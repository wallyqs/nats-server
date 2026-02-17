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

// rename-stream renames a JetStream stream on disk while the server is stopped.
//
// It updates the stream metadata, recomputes all integrity checksums (which are
// keyed by the stream name), removes the stream state file so the server rebuilds
// it on startup, and renames the stream directory.
//
// Usage:
//
//	go run util/rename-stream.go -dir /path/to/jetstream -account '$G' -old OLDNAME -new NEWNAME
//
// The server MUST be stopped before running this tool. After renaming, start the
// server and the stream will be available under the new name.
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/highwayhash"
)

const (
	jetstreamDir = "jetstream"
	metaFile     = "meta.inf"
	metaSumFile  = "meta.sum"
	msgsDir      = "msgs"
	stateFile    = "index.db"

	msgHdrSize = 22
	hashSize   = 8
	headerBit  = uint32(1 << 31)
)

func main() {
	var (
		storeDir string
		account  string
		oldName  string
		newName  string
		dryRun   bool
	)

	flag.StringVar(&storeDir, "dir", "", "JetStream store directory (the server's store_dir, e.g. /data/nats)")
	flag.StringVar(&account, "account", "$G", "Account name (default: $G for the global account)")
	flag.StringVar(&oldName, "old", "", "Current stream name")
	flag.StringVar(&newName, "new", "", "New stream name")
	flag.BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")
	flag.Parse()

	if storeDir == "" || oldName == "" || newName == "" {
		fmt.Fprintf(os.Stderr, "Usage: rename-stream -dir <store_dir> -old <current_name> -new <new_name> [-account <account>] [-dry-run]\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if oldName == newName {
		fmt.Fprintf(os.Stderr, "Error: old and new names are the same\n")
		os.Exit(1)
	}

	oldDir := filepath.Join(storeDir, jetstreamDir, account, "streams", oldName)
	newDir := filepath.Join(storeDir, jetstreamDir, account, "streams", newName)

	// Verify source exists.
	if _, err := os.Stat(oldDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: stream directory not found: %s\n", oldDir)
		os.Exit(1)
	}

	// Verify destination does not exist.
	if _, err := os.Stat(newDir); err == nil {
		fmt.Fprintf(os.Stderr, "Error: destination directory already exists: %s\n", newDir)
		os.Exit(1)
	}

	if dryRun {
		fmt.Printf("[dry-run] Would rename stream '%s' -> '%s'\n", oldName, newName)
		fmt.Printf("[dry-run] Source:      %s\n", oldDir)
		fmt.Printf("[dry-run] Destination: %s\n", newDir)
	}

	// Step 1: Update meta.inf with the new stream name.
	metaPath := filepath.Join(oldDir, metaFile)
	buf, err := os.ReadFile(metaPath)
	if err != nil {
		fatalf("Failed to read %s: %v", metaPath, err)
	}

	// Use generic JSON to avoid importing the server package.
	var cfg map[string]any
	if err := json.Unmarshal(buf, &cfg); err != nil {
		fatalf("Failed to parse %s: %v", metaPath, err)
	}

	currentName, _ := cfg["name"].(string)
	if currentName == "" {
		fatalf("Could not find 'name' field in %s", metaPath)
	}
	if currentName != oldName {
		fatalf("Stream name in %s is '%s', expected '%s'", metaPath, currentName, oldName)
	}

	cfg["name"] = newName
	newBuf, err := json.Marshal(cfg)
	if err != nil {
		fatalf("Failed to marshal updated config: %v", err)
	}

	if dryRun {
		fmt.Printf("[dry-run] Would update %s: name '%s' -> '%s'\n", metaFile, oldName, newName)
	} else {
		if err := os.WriteFile(metaPath, newBuf, 0600); err != nil {
			fatalf("Failed to write %s: %v", metaPath, err)
		}
		fmt.Printf("Updated %s\n", metaFile)
	}

	// Step 2: Recompute meta.sum using the new name as the hash key.
	// The recovery code uses the directory name for the hash key, and after
	// renaming the directory will be the new name.
	newKey := sha256.Sum256([]byte(newName))
	newHH, err := highwayhash.NewDigest64(newKey[:])
	if err != nil {
		fatalf("Failed to create hash digest: %v", err)
	}
	newHH.Write(newBuf)
	var hb [highwayhash.Size64]byte
	checksum := hex.EncodeToString(newHH.Sum(hb[:0]))

	sumPath := filepath.Join(oldDir, metaSumFile)
	if dryRun {
		fmt.Printf("[dry-run] Would update %s with new checksum\n", metaSumFile)
	} else {
		if err := os.WriteFile(sumPath, []byte(checksum), 0600); err != nil {
			fatalf("Failed to write %s: %v", sumPath, err)
		}
		fmt.Printf("Updated %s\n", metaSumFile)
	}

	// Step 3: Rewrite per-message checksums in all .blk files.
	// Each message record has an 8-byte highwayhash checksum at the end.
	// The hash key is sha256("streamName-blockIndex") — unique per block.
	blkDir := filepath.Join(oldDir, msgsDir)
	entries, err := os.ReadDir(blkDir)
	if err != nil {
		fatalf("Failed to read messages directory %s: %v", blkDir, err)
	}

	le := binary.LittleEndian
	blkCount := 0

	for _, fi := range entries {
		if !strings.HasSuffix(fi.Name(), ".blk") {
			continue
		}

		var blkIndex int
		if n, err := fmt.Sscanf(fi.Name(), "%d.blk", &blkIndex); err != nil || n != 1 {
			continue
		}

		blkPath := filepath.Join(blkDir, fi.Name())
		data, err := os.ReadFile(blkPath)
		if err != nil {
			fatalf("Failed to read %s: %v", blkPath, err)
		}
		if len(data) == 0 {
			continue
		}

		// Per-block hash key: sha256("newName-blockIndex")
		blkKey := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", newName, blkIndex)))
		blkHH, err := highwayhash.NewDigest64(blkKey[:])
		if err != nil {
			fatalf("Failed to create block hash digest: %v", err)
		}

		// Walk each record in the block and rewrite its checksum.
		records := 0
		for idx := uint32(0); idx < uint32(len(data)); {
			if idx+msgHdrSize > uint32(len(data)) {
				break
			}
			hdr := data[idx : idx+msgHdrSize]
			rl := le.Uint32(hdr[0:])
			hasHeaders := rl&headerBit != 0
			rl &^= headerBit
			if rl < msgHdrSize+hashSize || idx+rl > uint32(len(data)) {
				break
			}
			dlen := int(rl) - msgHdrSize
			slen := int(le.Uint16(hdr[20:]))
			rec := data[idx+msgHdrSize : idx+rl]

			shlen := slen
			if hasHeaders {
				shlen += 4
			}
			if dlen < hashSize || shlen > (dlen-hashSize) {
				break
			}

			// Compute new checksum over: sequence+timestamp (hdr[4:20]), subject,
			// and payload (with optional header length prefix).
			blkHH.Reset()
			blkHH.Write(hdr[4:20])
			blkHH.Write(rec[:slen])
			if hasHeaders {
				blkHH.Write(rec[slen+4 : dlen-hashSize])
			} else {
				blkHH.Write(rec[slen : dlen-hashSize])
			}
			copy(rec[dlen-hashSize:dlen], blkHH.Sum(hb[:0]))

			idx += rl
			records++
		}

		if dryRun {
			fmt.Printf("[dry-run] Would rewrite %d record checksums in %s\n", records, fi.Name())
		} else {
			if err := os.WriteFile(blkPath, data, 0600); err != nil {
				fatalf("Failed to write %s: %v", blkPath, err)
			}
			fmt.Printf("Rewrote %d record checksums in %s\n", records, fi.Name())
		}
		blkCount++
	}

	if blkCount == 0 {
		fmt.Println("Warning: no message block files found")
	}

	// Step 4: Remove the stream state file (index.db).
	// It contains block-level checksums keyed by the old name. The server will
	// rebuild it from the block files on startup.
	statePath := filepath.Join(blkDir, stateFile)
	if _, err := os.Stat(statePath); err == nil {
		if dryRun {
			fmt.Printf("[dry-run] Would remove %s (server will rebuild on startup)\n", stateFile)
		} else {
			if err := os.Remove(statePath); err != nil {
				fatalf("Failed to remove %s: %v", statePath, err)
			}
			fmt.Printf("Removed %s (server will rebuild on startup)\n", stateFile)
		}
	}

	// Step 5: Rename the directory.
	if dryRun {
		fmt.Printf("[dry-run] Would rename directory:\n  %s\n  -> %s\n", oldDir, newDir)
	} else {
		if err := os.Rename(oldDir, newDir); err != nil {
			fatalf("Failed to rename directory: %v", err)
		}
		fmt.Printf("Renamed directory:\n  %s\n  -> %s\n", oldDir, newDir)
	}

	fmt.Printf("\nStream '%s' renamed to '%s' successfully.\n", oldName, newName)
	if !dryRun {
		fmt.Println("You can now start the server.")
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
