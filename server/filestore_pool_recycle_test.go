// Copyright 2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !skip_store_tests

package server

import (
	"io"
	"testing"
	"time"
)

// resetBlkPoolCounters resets and returns a snapshot function. The snapshot
// function returns the delta of (gets, puts) since the reset.
func resetBlkPoolCounters() func() (int64, int64) {
	blkPoolGets.Store(0)
	blkPoolPuts.Store(0)
	return func() (int64, int64) {
		return blkPoolGets.Load(), blkPoolPuts.Load()
	}
}

// TestFileStorePoolRecycleRecoverMsgBlock verifies that recoverMsgBlock
// properly recycles pool buffers obtained via loadBlock for encrypted stores.
// Before the fix, the buffer from loadBlock(nil) was never recycled.
func TestFileStorePoolRecycleRecoverMsgBlock(t *testing.T) {
	// Only encrypted stores exercise the loadBlock path in recoverMsgBlock.
	for _, cipher := range []StoreCipher{AES, ChaCha} {
		for _, comp := range []StoreCompression{NoCompression, S2Compression} {
			fcfg := FileStoreConfig{
				StoreDir:    t.TempDir(),
				Cipher:      cipher,
				Compression: comp,
			}
			cfg := StreamConfig{Name: "zzz", Storage: FileStorage}
			t.Run(fcfg.Cipher.String()+"-"+fcfg.Compression.String(), func(t *testing.T) {
				created := time.Now()
				fs, err := newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
				require_NoError(t, err)

				// Store enough messages to have data.
				msg := make([]byte, 200)
				for i := 0; i < 10; i++ {
					_, _, err = fs.StoreMsg("foo", nil, msg, 0)
					require_NoError(t, err)
				}
				// Stop the store so we can reopen it and trigger recovery.
				err = fs.Stop()
				require_NoError(t, err)

				// Now reopen — this triggers recoverMsgBlock for each block.
				snap := resetBlkPoolCounters()
				fs, err = newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
				require_NoError(t, err)

				// Stop to flush all caches (clearCacheAndOffset -> recycleMsgBlockBuf).
				err = fs.Stop()
				require_NoError(t, err)

				gets, puts := snap()
				if gets != puts {
					t.Fatalf("Pool buffer leak in recoverMsgBlock: gets=%d puts=%d (leaked=%d)", gets, puts, gets-puts)
				}
			})
		}
	}
}

// TestFileStorePoolRecycleLoadMsgsWithLock verifies that loadMsgsWithLock
// properly recycles pool buffers on all code paths including when
// decompression creates a new buffer.
func TestFileStorePoolRecycleLoadMsgsWithLock(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		cfg := StreamConfig{Name: "zzz", Storage: FileStorage}
		created := time.Now()
		fs, err := newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
		require_NoError(t, err)

		// Store messages.
		msg := make([]byte, 200)
		for i := 0; i < 50; i++ {
			_, _, err = fs.StoreMsg("foo", nil, msg, 0)
			require_NoError(t, err)
		}

		// Flush caches so the next load goes through the full path.
		fs.mu.RLock()
		for _, mb := range fs.blks {
			mb.mu.Lock()
			mb.clearCacheAndOffset()
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		snap := resetBlkPoolCounters()

		// Force load all blocks via LoadMsg which calls loadMsgsWithLock.
		var smv StoreMsg
		for i := uint64(1); i <= 50; i++ {
			_, err = fs.LoadMsg(i, &smv)
			require_NoError(t, err)
		}

		// Stop to recycle all cached buffers.
		err = fs.Stop()
		require_NoError(t, err)

		gets, puts := snap()
		if gets != puts {
			t.Fatalf("Pool buffer leak in loadMsgsWithLock: gets=%d puts=%d (leaked=%d)", gets, puts, gets-puts)
		}
	})
}

// TestFileStorePoolRecycleCompact verifies that the fs-level compact function
// properly recycles pool buffers (the nbuf used for rewriting blocks).
func TestFileStorePoolRecycleCompact(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 512
		cfg := StreamConfig{Name: "zzz", Storage: FileStorage}
		created := time.Now()
		fs, err := newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
		require_NoError(t, err)

		// Store messages to create multiple blocks.
		msg := make([]byte, 100)
		for i := 0; i < 50; i++ {
			_, _, err = fs.StoreMsg("foo", nil, msg, 0)
			require_NoError(t, err)
		}
		state := fs.State()

		// Flush caches so we start clean.
		fs.mu.RLock()
		for _, mb := range fs.blks {
			mb.mu.Lock()
			mb.clearCacheAndOffset()
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		snap := resetBlkPoolCounters()

		// Compact from the last sequence to compact away all but the last block.
		// Blocks that become empty go through dirtyCloseWithRemove which
		// properly recycles cached buffers via clearCacheAndOffset.
		_, err = fs.Compact(state.LastSeq)
		require_NoError(t, err)

		// Stop to recycle remaining cached buffers.
		err = fs.Stop()
		require_NoError(t, err)

		gets, puts := snap()
		// compact may have 1 leaked buffer from finishedWithCache on the
		// partial block (the one containing the compact target seq).
		// Blocks fully removed are properly recycled via dirtyCloseWithRemove.
		if gets-puts > 1 {
			t.Fatalf("Pool buffer leak in compact: gets=%d puts=%d (leaked=%d, expected at most 1 from finishedWithCache)",
				gets, puts, gets-puts)
		}
	})
}

// TestFileStorePoolRecycleTruncate verifies that truncate properly recycles
// pool buffers from loadBlock on all code paths (encrypted, compressed, etc).
func TestFileStorePoolRecycleTruncate(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 4096
		cfg := StreamConfig{Name: "zzz", Subjects: []string{"*"}, Storage: FileStorage}
		created := time.Now()
		fs, err := newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
		require_NoError(t, err)

		// Store enough messages to span multiple blocks.
		msg := make([]byte, 200)
		for i := 0; i < 50; i++ {
			_, _, err = fs.StoreMsg("foo", nil, msg, 0)
			require_NoError(t, err)
		}

		// Flush all caches so we start from a clean state.
		fs.mu.RLock()
		for _, mb := range fs.blks {
			mb.mu.Lock()
			mb.clearCacheAndOffset()
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		snap := resetBlkPoolCounters()

		// Truncate to sequence 25 — for compressed/encrypted stores this
		// exercises the loadBlock + decompressIfNeeded path in truncate.
		err = fs.Truncate(25)
		require_NoError(t, err)

		// Stop to recycle all cached buffers.
		err = fs.Stop()
		require_NoError(t, err)

		gets, puts := snap()
		// truncate calls finishedWithCache which drops the cache reference
		// without recycling the pool buffer. This is a known separate issue
		// that causes exactly 1 buffer leak per truncated block. The fix
		// ensures the second loadBlock buffer (for encrypted/compressed
		// blocks) IS properly recycled. Without the fix, the leak would
		// be larger.
		isEncOrComp := fcfg.Cipher != NoCipher || fcfg.Compression != NoCompression
		if isEncOrComp {
			// loadMsgsWithLock (1 get → cache, leaked by finishedWithCache)
			// + loadBlock (1 get → recycled by fix) = 1 leaked buffer.
			// Removed blocks are properly cleaned up via dirtyCloseWithRemove.
			if gets-puts > 1 {
				t.Fatalf("Pool buffer leak in truncate: gets=%d puts=%d (leaked=%d, expected at most 1 from finishedWithCache)",
					gets, puts, gets-puts)
			}
		} else {
			// Unencrypted/uncompressed truncate uses file truncation,
			// no loadBlock call. The finishedWithCache leak may still occur.
			if gets-puts > 1 {
				t.Fatalf("Pool buffer leak in truncate: gets=%d puts=%d (leaked=%d, expected at most 1 from finishedWithCache)",
					gets, puts, gets-puts)
			}
		}
	})
}

// TestFileStorePoolRecycleStreamSnapshot verifies that streamSnapshot
// properly recycles pool buffers across all block iterations.
func TestFileStorePoolRecycleStreamSnapshot(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 512
		cfg := StreamConfig{Name: "zzz", Subjects: []string{"foo"}, Storage: FileStorage}
		created := time.Now()
		fs, err := newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
		require_NoError(t, err)

		// Store messages across multiple blocks.
		msg := make([]byte, 100)
		for i := 0; i < 50; i++ {
			_, _, err = fs.StoreMsg("foo", nil, msg, 0)
			require_NoError(t, err)
		}

		snap := resetBlkPoolCounters()

		// Create a snapshot — this iterates all blocks, loading each.
		sr, err := fs.Snapshot(5*time.Second, true, true)
		require_NoError(t, err)

		// Read and discard the snapshot to complete the operation.
		_, err = io.ReadAll(sr.Reader)
		require_NoError(t, err)

		// Stop to recycle remaining cached buffers.
		err = fs.Stop()
		require_NoError(t, err)

		gets, puts := snap()
		if gets != puts {
			t.Fatalf("Pool buffer leak in streamSnapshot: gets=%d puts=%d (leaked=%d)", gets, puts, gets-puts)
		}
	})
}

// TestFileStorePoolRecycleConvertToEncrypted verifies that convertToEncrypted
// properly recycles the pool buffer obtained via loadBlock.
func TestFileStorePoolRecycleConvertToEncrypted(t *testing.T) {
	for _, cipher := range []StoreCipher{AES, ChaCha} {
		for _, comp := range []StoreCompression{NoCompression, S2Compression} {
			t.Run(cipher.String()+"-"+comp.String(), func(t *testing.T) {
				// First create an unencrypted store.
				fcfgPlain := FileStoreConfig{
					StoreDir:    t.TempDir(),
					Cipher:      NoCipher,
					Compression: comp,
				}
				cfg := StreamConfig{Name: "zzz", Storage: FileStorage}
				created := time.Now()
				fs, err := newFileStoreWithCreated(fcfgPlain, cfg, created, nil, nil)
				require_NoError(t, err)

				msg := make([]byte, 200)
				for i := 0; i < 10; i++ {
					_, _, err = fs.StoreMsg("foo", nil, msg, 0)
					require_NoError(t, err)
				}
				err = fs.Stop()
				require_NoError(t, err)

				// Reopen with encryption — this triggers convertToEncrypted for each block.
				fcfgEnc := FileStoreConfig{
					StoreDir:    fcfgPlain.StoreDir,
					Cipher:      cipher,
					Compression: comp,
				}
				snap := resetBlkPoolCounters()
				fs, err = newFileStoreWithCreated(fcfgEnc, cfg, created, prf(&fcfgEnc), nil)
				require_NoError(t, err)
				err = fs.Stop()
				require_NoError(t, err)

				gets, puts := snap()
				if gets != puts {
					t.Fatalf("Pool buffer leak in convertToEncrypted: gets=%d puts=%d (leaked=%d)", gets, puts, gets-puts)
				}
			})
		}
	}
}

// TestFileStorePoolRecycleConvertCipher verifies that convertCipher
// properly recycles the pool buffer obtained via loadBlock.
func TestFileStorePoolRecycleConvertCipher(t *testing.T) {
	for _, comp := range []StoreCompression{NoCompression, S2Compression} {
		t.Run(comp.String(), func(t *testing.T) {
			// Create store with AES encryption.
			fcfgAES := FileStoreConfig{
				StoreDir:    t.TempDir(),
				Cipher:      AES,
				Compression: comp,
			}
			cfg := StreamConfig{Name: "zzz", Storage: FileStorage}
			created := time.Now()
			fs, err := newFileStoreWithCreated(fcfgAES, cfg, created, prf(&fcfgAES), nil)
			require_NoError(t, err)

			msg := make([]byte, 200)
			for i := 0; i < 10; i++ {
				_, _, err = fs.StoreMsg("foo", nil, msg, 0)
				require_NoError(t, err)
			}
			err = fs.Stop()
			require_NoError(t, err)

			// Reopen with ChaCha — this triggers convertCipher for each block.
			fcfgChaCha := FileStoreConfig{
				StoreDir:    fcfgAES.StoreDir,
				Cipher:      ChaCha,
				Compression: comp,
			}
			snap := resetBlkPoolCounters()
			fs, err = newFileStoreWithCreated(fcfgChaCha, cfg, created, prf(&fcfgChaCha), nil)
			require_NoError(t, err)
			err = fs.Stop()
			require_NoError(t, err)

			gets, puts := snap()
			if gets != puts {
				t.Fatalf("Pool buffer leak in convertCipher: gets=%d puts=%d (leaked=%d)", gets, puts, gets-puts)
			}
		})
	}
}

// TestFileStorePoolRecycleLastChecksum verifies that lastChecksum properly
// recycles the pool buffer obtained via loadBlock for encrypted stores.
func TestFileStorePoolRecycleLastChecksum(t *testing.T) {
	// lastChecksum is called during rebuildStateLocked and recoverMsgBlock.
	// We trigger it via recovery of an encrypted store with multiple blocks.
	for _, cipher := range []StoreCipher{AES, ChaCha} {
		for _, comp := range []StoreCompression{NoCompression, S2Compression} {
			fcfg := FileStoreConfig{
				StoreDir:    t.TempDir(),
				Cipher:      cipher,
				Compression: comp,
				BlockSize:   512,
			}
			cfg := StreamConfig{Name: "zzz", Storage: FileStorage}
			t.Run(fcfg.Cipher.String()+"-"+fcfg.Compression.String(), func(t *testing.T) {
				created := time.Now()
				fs, err := newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
				require_NoError(t, err)

				// Use small block size and many messages to create multiple blocks.
				msg := make([]byte, 100)
				for i := 0; i < 50; i++ {
					_, _, err = fs.StoreMsg("foo", nil, msg, 0)
					require_NoError(t, err)
				}
				err = fs.Stop()
				require_NoError(t, err)

				// Reopen triggers recovery which uses lastChecksum.
				snap := resetBlkPoolCounters()
				fs, err = newFileStoreWithCreated(fcfg, cfg, created, prf(&fcfg), nil)
				require_NoError(t, err)
				err = fs.Stop()
				require_NoError(t, err)

				gets, puts := snap()
				if gets != puts {
					t.Fatalf("Pool buffer leak in lastChecksum/recovery: gets=%d puts=%d (leaked=%d)", gets, puts, gets-puts)
				}
			})
		}
	}
}
