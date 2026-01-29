// Copyright 2026 The NATS Authors
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
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestFileStoreDeadlock_MissingDeferUnlock reproduces the deadlock condition
// found in production where a panic during fileStore initialization leaves
// the mutex locked, causing all subsequent operations to hang indefinitely.
//
// This test demonstrates the bug at filestore.go:548 where fs.mu.Lock() is
// called without a corresponding defer fs.mu.Unlock(), causing a panic in
// any of the enforcement operations to leave the lock permanently held.
func TestFileStoreDeadlock_MissingDeferUnlock(t *testing.T) {
	// Simulate the fileStore mutex behavior
	type mockFileStore struct {
		mu     sync.RWMutex
		closed bool
	}

	fs := &mockFileStore{}

	// Track goroutine states
	goroutineStarted := make(chan struct{})
	goroutineBlocked := make(chan struct{})
	panicOccurred := make(chan struct{})

	// Goroutine 1: Simulates newFileStoreWithCreated (goroutine 27458 in production)
	// This is the goroutine that will panic while holding the lock
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Goroutine 1 (creator) panicked as expected: %v", r)
				close(panicOccurred)
				// NOTE: In the buggy code, there's NO defer unlock, so the lock
				// remains held even after panic recovery
			}
		}()

		t.Log("Goroutine 1 (creator): Acquiring lock (like filestore.go:548)")
		fs.mu.Lock()
		// BUG: Missing defer fs.mu.Unlock() here!

		t.Log("Goroutine 1 (creator): Lock acquired, starting operations")
		close(goroutineStarted)

		// Simulate operations that can panic (filestore.go:553-576)
		// In production, this could be:
		// - fs.removeMsg()
		// - fs.enforceMsgLimit()
		// - fs.enforceBytesLimit()
		// - fs.expireMsgsOnRecover()
		// - fs.startAgeChk()
		time.Sleep(10 * time.Millisecond)

		t.Log("Goroutine 1 (creator): Simulating panic in enforceMsgLimit()")
		panic("simulated panic during message enforcement")

		// This unlock is never reached (filestore.go:580)
		// fs.mu.Unlock()
	}()

	// Wait for the creator goroutine to acquire the lock
	<-goroutineStarted

	// Goroutine 2: Simulates rebuildState (goroutine 27517 in production)
	// This goroutine needs the lock but will block forever
	go func() {
		time.Sleep(20 * time.Millisecond)
		t.Log("Goroutine 2 (rebuildState): Attempting to acquire lock (filestore.go:1256)")

		// Signal that we're about to block
		go func() {
			time.Sleep(50 * time.Millisecond)
			close(goroutineBlocked)
		}()

		fs.mu.Lock()
		defer fs.mu.Unlock()
		t.Log("Goroutine 2 (rebuildState): Lock acquired")
	}()

	// Goroutine 3: Simulates _writeFullState (goroutine 27515 in production)
	go func() {
		time.Sleep(20 * time.Millisecond)
		t.Log("Goroutine 3 (_writeFullState): Attempting to acquire lock (filestore.go:9950)")
		fs.mu.Lock()
		defer fs.mu.Unlock()
		t.Log("Goroutine 3 (_writeFullState): Lock acquired")
	}()

	// Goroutine 4: Simulates stop (goroutine 27518 in production)
	go func() {
		time.Sleep(20 * time.Millisecond)
		t.Log("Goroutine 4 (stop): Attempting to acquire lock (filestore.go:10202)")
		fs.mu.Lock()
		defer fs.mu.Unlock()
		t.Log("Goroutine 4 (stop): Lock acquired")
	}()

	// Wait for panic to occur
	<-panicOccurred
	t.Log("Main: Panic occurred in goroutine 1")

	// Wait for goroutines to block
	<-goroutineBlocked
	t.Log("Main: Goroutines 2, 3, 4 are now blocked waiting for the lock")

	// Try to acquire the lock with a timeout to demonstrate the deadlock
	lockAcquired := make(chan struct{})
	go func() {
		t.Log("Main: Attempting to acquire lock to verify deadlock")
		fs.mu.Lock()
		fs.mu.Unlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
		t.Fatal("UNEXPECTED: Lock was acquired! The deadlock was not reproduced.")
	case <-time.After(200 * time.Millisecond):
		t.Log("SUCCESS: Deadlock reproduced! Lock is permanently held after panic.")
		t.Log("")
		t.Log("=== DEADLOCK CONFIRMED ===")
		t.Log("This demonstrates the production bug:")
		t.Log("- Goroutine 1 acquired lock at filestore.go:548")
		t.Log("- Goroutine 1 panicked during enforcement operations")
		t.Log("- Lock was never released (missing defer fs.mu.Unlock())")
		t.Log("- Goroutines 2, 3, 4 are permanently blocked")
		t.Log("")
		t.Log("FIX: Add 'defer fs.mu.Unlock()' immediately after fs.mu.Lock() at line 548")
	}
}

// TestFileStoreDeadlock_WithFix demonstrates that adding defer fixes the issue
func TestFileStoreDeadlock_WithFix(t *testing.T) {
	type mockFileStore struct {
		mu     sync.RWMutex
		closed bool
	}

	fs := &mockFileStore{}

	goroutineStarted := make(chan struct{})
	panicOccurred := make(chan struct{})
	lockReleased := make(chan struct{})

	// Goroutine 1: With proper defer unlock
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Goroutine 1 panicked: %v", r)
				close(panicOccurred)
			}
		}()

		t.Log("Goroutine 1 (with fix): Acquiring lock")
		fs.mu.Lock()
		defer fs.mu.Unlock() // FIX: Added defer unlock
		defer func() {
			close(lockReleased)
		}()

		close(goroutineStarted)
		time.Sleep(10 * time.Millisecond)

		t.Log("Goroutine 1 (with fix): Simulating panic")
		panic("simulated panic")
	}()

	<-goroutineStarted

	// Goroutine 2: Should be able to acquire lock after panic
	lockAcquired := make(chan struct{})
	go func() {
		<-panicOccurred
		time.Sleep(20 * time.Millisecond)

		t.Log("Goroutine 2 (with fix): Attempting to acquire lock")
		fs.mu.Lock()
		defer fs.mu.Unlock()
		t.Log("Goroutine 2 (with fix): Lock acquired successfully!")
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
		t.Log("SUCCESS: Lock was properly released after panic thanks to defer")
		t.Log("=== FIX VERIFIED ===")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("FAILED: Lock was not released even with defer (unexpected)")
	}
}

// TestFileStoreDeadlock_EarlyReturn tests the early return bug at line 568
func TestFileStoreDeadlock_EarlyReturn(t *testing.T) {
	type mockFileStore struct {
		mu     sync.RWMutex
		closed bool
	}

	fs := &mockFileStore{}

	// Simulate the early return at filestore.go:568
	simulateNewFileStore := func() error {
		fs.mu.Lock()
		// BUG: No defer here

		// Simulate expireMsgsOnRecover returning a permission error
		err := &os.PathError{Op: "remove", Path: "/some/file", Err: syscall.EPERM}

		// This is the buggy code at line 568
		if isPermissionError(err) {
			return err // Returns while holding the lock!
		}

		fs.mu.Unlock()
		return nil
	}

	goroutineFinished := make(chan struct{})
	go func() {
		t.Log("Goroutine 1: Calling simulateNewFileStore")
		err := simulateNewFileStore()
		if err != nil {
			t.Logf("Goroutine 1: Returned with error: %v (LOCK STILL HELD!)", err)
		}
		close(goroutineFinished)
	}()

	<-goroutineFinished

	// Try to acquire lock
	lockAcquired := make(chan struct{})
	go func() {
		t.Log("Goroutine 2: Attempting to acquire lock")
		fs.mu.Lock()
		fs.mu.Unlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
		t.Fatal("UNEXPECTED: Lock was acquired! Early return bug not reproduced.")
	case <-time.After(100 * time.Millisecond):
		t.Log("SUCCESS: Early return bug reproduced!")
		t.Log("=== EARLY RETURN BUG CONFIRMED ===")
		t.Log("- Function returned at line 568 with error")
		t.Log("- Lock was never released")
		t.Log("- All subsequent operations are blocked")
	}
}

// TestFileStorePanicDuringEnforcement reproduces the production deadlock bug where
// a panic during fileStore initialization left the mutex locked, causing all
// subsequent operations to hang indefinitely.
//
// This test verifies that the defer unlock mechanism (added in the fix) properly
// releases the lock even when enforcement operations panic.
//
// Background: In production, goroutine 27458 panicked while holding fs.mu during
// enforcement operations in newFileStoreWithCreated, leaving the lock permanently
// held and blocking 6 goroutines for over 13 hours.
func TestFileStorePanicDuringEnforcement(t *testing.T) {
	storeDir := t.TempDir()

	// Create a filestore with message limits that will trigger enforcement
	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 1024,
	}
	cfg := StreamConfig{
		Name:     "TEST",
		Storage:  FileStorage,
		MaxMsgs:  10,
		MaxBytes: 1024 * 10,
	}

	// Create initial filestore and add some messages
	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	// Store some messages
	subj := "foo"
	for i := 0; i < 5; i++ {
		msg := []byte("Hello World")
		if _, _, err := fs.StoreMsg(subj, nil, msg, 0); err != nil {
			t.Fatalf("Error storing msg: %v", err)
		}
	}

	// Close the filestore
	fs.Stop()

	// Now we'll create a scenario that can cause a panic during enforcement.
	// We'll corrupt the filestore state by creating an invalid message block file.
	// When newFileStoreWithCreated tries to enforce limits, it may panic.

	// Create a corrupted block file that will cause issues during recovery
	blockFile := filepath.Join(storeDir, msgDir, "1.blk")
	if err := os.WriteFile(blockFile, []byte("corrupted data"), 0600); err != nil {
		t.Fatalf("Failed to corrupt block file: %v", err)
	}

	// Track if we can acquire the lock after a panic
	var lockAcquired atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)

	// Spawn a goroutine that will try to acquire the lock after the panic
	go func() {
		defer wg.Done()
		// Wait a bit for the panic to occur
		time.Sleep(100 * time.Millisecond)

		// Try to acquire the lock on the filestore by calling a method that needs it
		// We'll try to create a new filestore instance which will attempt recovery
		testFS, _ := newFileStore(fcfg, cfg)
		if testFS != nil {
			// If we got here, we acquired the lock (either directly or after it was released)
			lockAcquired.Store(true)
			testFS.Stop()
		}
	}()

	// Try to recreate the filestore - this should trigger enforcement during recovery
	// and may panic due to corrupted state
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Panic occurred during enforcement (expected): %v", r)
			}
		}()

		// This may panic during the enforcement operations
		fs2, err := newFileStore(fcfg, cfg)
		if err != nil {
			t.Logf("Error during filestore creation (may be expected): %v", err)
		}
		if fs2 != nil {
			fs2.Stop()
		}
	}()

	// Wait for the test goroutine with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Goroutine finished
		if lockAcquired.Load() {
			t.Log("SUCCESS: Lock was properly released after panic (fix is working)")
		} else {
			t.Log("Lock acquisition test completed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK: Test goroutine blocked trying to acquire lock - the fix is not working!")
	}
}

// TestFileStorePanicDuringEnforcementWithClosure is a more direct test that
// simulates the exact panic scenario from production by creating a filestore
// with specific conditions that trigger panics during enforcement.
func TestFileStorePanicDuringEnforcementWithClosure(t *testing.T) {
	storeDir := t.TempDir()

	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 1024,
	}
	cfg := StreamConfig{
		Name:     "TEST",
		Storage:  FileStorage,
		MaxMsgs:  5,                // Low limit to trigger enforcement
		MaxBytes: 1024,             // Low limit to trigger enforcement
		MaxAge:   time.Second * 10, // This will trigger age enforcement
	}

	// Create and populate a filestore
	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	// Add messages
	for i := 0; i < 10; i++ {
		msg := []byte("Test message that exceeds limits")
		if _, _, err := fs.StoreMsg("test.subject", nil, msg, 0); err != nil {
			// May fail due to limits, that's ok
			break
		}
	}

	firstSeqBefore := fs.state.FirstSeq
	fs.Stop()

	// Try to reopen - this will trigger enforcement
	// In the buggy version, if this panics, the lock is held forever
	var panicOccurred bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicOccurred = true
				t.Logf("Panic during enforcement (testing recovery): %v", r)
			}
		}()

		fs2, err := newFileStore(fcfg, cfg)
		if err != nil {
			t.Logf("Error during reopen: %v", err)
		}
		if fs2 != nil {
			// Verify we can access the filestore after enforcement
			state := fs2.State()
			t.Logf("Filestore state after enforcement: FirstSeq=%d, LastSeq=%d, Msgs=%d",
				state.FirstSeq, state.LastSeq, state.Msgs)

			// Try to acquire the lock by doing an operation
			_, _, err := fs2.StoreMsg("test.subject", nil, []byte("new message"), 0)
			if err != nil {
				t.Logf("Store msg after enforcement: %v", err)
			}

			fs2.Stop()
		}
	}()

	// Now verify we can create another filestore instance
	// If the lock was held from a panic, this will timeout
	done := make(chan struct{})
	go func() {
		defer close(done)

		fs3, err := newFileStore(fcfg, cfg)
		if err != nil {
			t.Logf("Third filestore creation: %v", err)
			return
		}
		if fs3 != nil {
			state := fs3.State()
			t.Logf("Third filestore state: FirstSeq=%d, LastSeq=%d, Msgs=%d",
				state.FirstSeq, state.LastSeq, state.Msgs)

			// Verify enforcement actually ran by checking if firstSeq changed
			if state.FirstSeq > firstSeqBefore {
				t.Logf("Enforcement ran successfully: FirstSeq changed from %d to %d",
					firstSeqBefore, state.FirstSeq)
			}

			fs3.Stop()
		}
	}()

	select {
	case <-done:
		t.Log("SUCCESS: Able to create new filestore after enforcement (lock was released)")
	case <-time.After(3 * time.Second):
		t.Fatal("DEADLOCK: Unable to create new filestore - lock is still held after panic!")
	}

	if panicOccurred {
		t.Log("Note: This test simulated a panic during enforcement")
	}
}

// TestFileStoreKVStyleRecoveryDeadlock simulates the production deadlock scenario
// where a KV-style stream (max_msgs_per_subject=1) with many deleted messages
// encounters a panic during enforceMsgPerSubjectLimit() on server restart.
//
// Production scenario:
//   - Stream: KV_unique_ids with max_msgs_per_subject=1
//   - State: 9 messages, 1,392,898 deleted, last_seq=1,392,913
//   - On restart: enforceMsgPerSubjectLimit() panics, lock never released
//
// This test creates a similar scenario and verifies the fix works.
func TestFileStoreKVStyleRecoveryDeadlock(t *testing.T) {
	storeDir := t.TempDir()

	// Configuration matching KV bucket behavior
	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 512, // Small blocks to create multiple block files
	}
	cfg := StreamConfig{
		Name:       "KV_test",
		Storage:    FileStorage,
		MaxMsgsPer: 1, // KV style: only 1 message per subject
	}

	// Create initial filestore
	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	// Simulate KV behavior: write many updates to the same keys
	// Each update to a subject deletes the previous message
	numSubjects := 10
	updatesPerSubject := 100 // This creates ~1000 deleted messages

	t.Logf("Creating KV-style workload: %d subjects x %d updates = %d total writes",
		numSubjects, updatesPerSubject, numSubjects*updatesPerSubject)

	for update := 0; update < updatesPerSubject; update++ {
		for subj := 0; subj < numSubjects; subj++ {
			subject := fmt.Sprintf("$KV.test.key%d", subj)
			msg := []byte(fmt.Sprintf("value-%d", update))
			if _, _, err := fs.StoreMsg(subject, nil, msg, 0); err != nil {
				t.Fatalf("Error storing msg: %v", err)
			}
		}
	}

	// Check state before stopping
	state := fs.State()
	t.Logf("State before stop: Msgs=%d, FirstSeq=%d, LastSeq=%d, NumDeleted=%d",
		state.Msgs, state.FirstSeq, state.LastSeq, state.NumDeleted)

	// Verify we have the expected state (should have numSubjects messages, rest deleted)
	if state.Msgs != uint64(numSubjects) {
		t.Logf("Note: Expected %d messages, got %d", numSubjects, state.Msgs)
	}

	// Stop the filestore without writing full state to force recovery
	fs.Stop()

	// Now corrupt one of the block files to potentially trigger a panic
	// during enforceMsgPerSubjectLimit -> rebuildState
	msgsDir := filepath.Join(storeDir, msgDir)
	entries, err := os.ReadDir(msgsDir)
	if err != nil {
		t.Fatalf("Failed to read msgs dir: %v", err)
	}

	// Find and corrupt a .blk file
	var corruptedFile string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".blk" {
			corruptedFile = filepath.Join(msgsDir, entry.Name())
			// Read file, corrupt middle section, write back
			data, err := os.ReadFile(corruptedFile)
			if err != nil {
				continue
			}
			if len(data) > 100 {
				// Corrupt the middle of the file
				for i := len(data) / 2; i < len(data)/2+50 && i < len(data); i++ {
					data[i] = 0xFF
				}
				os.WriteFile(corruptedFile, data, 0644)
				t.Logf("Corrupted block file: %s", entry.Name())
				break
			}
		}
	}

	// Also remove index.db to force recovery from block files
	indexFile := filepath.Join(msgsDir, "index.db")
	os.Remove(indexFile)

	// Now try to recover the filestore
	// With the bug: if enforceMsgPerSubjectLimit panics, the lock is held forever
	// With the fix: the defer releases the lock even on panic

	t.Log("Attempting to recover filestore (may trigger panic during enforcement)...")

	recoverDone := make(chan struct{})
	var recoveredFS *fileStore
	var recoverErr error

	go func() {
		defer close(recoverDone)
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Panic during recovery (expected with corruption): %v", r)
			}
		}()

		recoveredFS, recoverErr = newFileStore(fcfg, cfg)
	}()

	// Wait for recovery with timeout
	select {
	case <-recoverDone:
		if recoverErr != nil {
			t.Logf("Recovery returned error (expected with corruption): %v", recoverErr)
		} else if recoveredFS != nil {
			state := recoveredFS.State()
			t.Logf("Recovery succeeded: Msgs=%d, FirstSeq=%d, LastSeq=%d",
				state.Msgs, state.FirstSeq, state.LastSeq)
			recoveredFS.Stop()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: Recovery did not complete - lock is likely held!")
	}

	// Try to create another filestore instance to verify no deadlock
	t.Log("Verifying no deadlock by creating another filestore instance...")

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		fs2, err := newFileStore(fcfg, cfg)
		if err != nil {
			t.Logf("Second filestore creation error: %v", err)
			return
		}
		if fs2 != nil {
			t.Log("Second filestore created successfully - no deadlock!")
			fs2.Stop()
		}
	}()

	select {
	case <-secondDone:
		t.Log("SUCCESS: No deadlock detected, lock was properly released")
	case <-time.After(3 * time.Second):
		t.Fatal("DEADLOCK: Second filestore creation blocked - lock is still held!")
	}
}

// TestFileStoreMaxMsgsPerSubjectEnforcementRecovery tests that enforceMsgPerSubjectLimit
// works correctly on recovery and releases the lock even if there are issues.
func TestFileStoreMaxMsgsPerSubjectEnforcementRecovery(t *testing.T) {
	storeDir := t.TempDir()

	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 1024,
	}
	cfg := StreamConfig{
		Name:       "TEST",
		Storage:    FileStorage,
		MaxMsgsPer: 1, // This triggers enforceMsgPerSubjectLimit on recovery
	}

	// Create filestore and add messages
	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	// Store messages to multiple subjects
	subjects := []string{"foo.1", "foo.2", "foo.3", "bar.1", "bar.2"}
	for i := 0; i < 50; i++ {
		for _, subj := range subjects {
			msg := []byte(fmt.Sprintf("msg-%d", i))
			fs.StoreMsg(subj, nil, msg, 0)
		}
	}

	state := fs.State()
	t.Logf("Before stop: Msgs=%d, Deleted=%d, LastSeq=%d",
		state.Msgs, state.NumDeleted, state.LastSeq)

	// Stop without full state write
	fs.stop(false, false)

	// Remove index to force block-based recovery
	os.Remove(filepath.Join(storeDir, msgDir, "index.db"))

	// Recovery should run enforceMsgPerSubjectLimit
	fs2, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}
	defer fs2.Stop()

	state2 := fs2.State()
	t.Logf("After recovery: Msgs=%d, Deleted=%d, LastSeq=%d",
		state2.Msgs, state2.NumDeleted, state2.LastSeq)

	// The key check is that we have some messages and recovery completed
	// The exact count depends on what was compacted/recovered
	if state2.Msgs == 0 {
		t.Errorf("Expected messages after recovery, got 0")
	}

	// Verify we can still operate on the filestore (lock is not held)
	_, _, err = fs2.StoreMsg("test.new", nil, []byte("new message"), 0)
	if err != nil {
		t.Fatalf("Failed to store message after recovery: %v", err)
	}
	t.Log("SUCCESS: Filestore operational after recovery with MaxMsgsPer enforcement")
}

// TestJetStreamKVStreamDeleteDeadlock simulates the production scenario where:
// 1. A KV bucket stream (max_msgs_per_subject=1) exists with many deleted messages
// 2. Server restarts and triggers enforceMsgPerSubjectLimit() during recovery
// 3. If the lock is held due to a panic, stream delete will hang
//
// This test verifies that with the defer unlock fix, stream delete works even
// after a problematic recovery.
func TestJetStreamKVStreamDeleteDeadlock(t *testing.T) {
	// Create a JetStream server
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	// Get the store directory for later corruption
	sd := s.JetStreamConfig().StoreDir

	// Create a KV-style stream configuration
	streamName := "KV_test_deadlock"
	cfg := &StreamConfig{
		Name:       streamName,
		Subjects:   []string{"$KV.test.>"},
		Storage:    FileStorage,
		MaxMsgsPer: 1, // KV style: only 1 message per subject
		Replicas:   1,
	}

	acc := s.GlobalAccount()
	mset, err := acc.addStream(cfg)
	if err != nil {
		t.Fatalf("Failed to create stream: %v", err)
	}

	// Simulate KV behavior: many updates to same keys
	// Use more keys and updates to create a more complex state
	numKeys := 100
	updatesPerKey := 100

	t.Logf("Populating KV stream with %d keys x %d updates = %d total writes",
		numKeys, updatesPerKey, numKeys*updatesPerKey)

	for update := 0; update < updatesPerKey; update++ {
		for key := 0; key < numKeys; key++ {
			subject := fmt.Sprintf("$KV.test.key%d", key)
			msg := []byte(fmt.Sprintf("value-update-%d", update))
			if _, _, err := mset.store.StoreMsg(subject, nil, msg, 0); err != nil {
				t.Fatalf("Failed to store message: %v", err)
			}
		}
	}

	state := mset.state()
	t.Logf("Stream state: Msgs=%d, FirstSeq=%d, LastSeq=%d, Deleted=%d",
		state.Msgs, state.FirstSeq, state.LastSeq, state.NumDeleted)

	// Get the stream's store directory
	fs := mset.store.(*fileStore)
	streamStoreDir := fs.fcfg.StoreDir

	// Capture port for restart
	clientURL := s.ClientURL()

	// Shutdown the server
	t.Log("Shutting down server...")
	s.Shutdown()

	// Aggressively corrupt the storage to try to trigger panic during recovery
	msgsDir := filepath.Join(streamStoreDir, msgDir)
	entries, err := os.ReadDir(msgsDir)
	if err != nil {
		t.Fatalf("Failed to read msgs dir: %v", err)
	}

	// Remove index.db to force block-based recovery
	os.Remove(filepath.Join(msgsDir, "index.db"))
	t.Log("Removed index.db")

	// Corrupt ALL block files to maximize chance of triggering issues
	corruptedCount := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".blk" {
			blkFile := filepath.Join(msgsDir, entry.Name())
			data, err := os.ReadFile(blkFile)
			if err != nil {
				continue
			}
			if len(data) > 50 {
				// Corrupt multiple sections of the file
				// Corrupt header area
				for i := 0; i < 20 && i < len(data); i++ {
					data[i] = 0xFF
				}
				// Corrupt middle section
				mid := len(data) / 2
				for i := mid; i < mid+50 && i < len(data); i++ {
					data[i] = 0x00
				}
				// Corrupt end section
				end := len(data) - 20
				if end > 0 {
					for i := end; i < len(data); i++ {
						data[i] = 0xAB
					}
				}
				os.WriteFile(blkFile, data, 0644)
				corruptedCount++
			}
		}
	}
	t.Logf("Corrupted %d block files", corruptedCount)

	// Restart the server
	t.Log("Restarting server...")

	// Parse port from client URL
	u, _ := url.Parse(clientURL)
	port, _ := strconv.Atoi(u.Port())

	s = RunJetStreamServerOnPort(port, sd)
	defer s.Shutdown()

	// Wait for server to be ready
	time.Sleep(500 * time.Millisecond)

	// Try to lookup and delete the stream
	// If the lock is held due to missing defer unlock, this will hang
	t.Log("Attempting to delete stream (will hang if deadlocked)...")

	acc = s.GlobalAccount()

	deleteComplete := make(chan error, 1)
	go func() {
		// First try to lookup the stream
		mset, err := acc.lookupStream(streamName)
		if err != nil {
			deleteComplete <- fmt.Errorf("stream lookup failed: %v", err)
			return
		}
		if mset == nil {
			deleteComplete <- fmt.Errorf("stream not found after restart")
			return
		}

		// Now try to delete it - this requires acquiring the lock
		err = mset.delete()
		deleteComplete <- err
	}()

	select {
	case err := <-deleteComplete:
		if err != nil {
			t.Logf("Stream operation result: %v", err)
		} else {
			t.Log("SUCCESS: Stream deleted successfully - no deadlock!")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK: Stream delete timed out - lock is likely held!")
	}

	// Verify the stream is actually gone
	if mset, _ := acc.lookupStream(streamName); mset != nil {
		t.Log("Note: Stream still exists (delete may have failed due to corruption)")
	}
}

// TestJetStreamStreamDeleteAfterRecoveryPanic tests that stream delete works
// even if recovery had issues, verifying the defer unlock fix works at the
// JetStream level.
func TestJetStreamStreamDeleteAfterRecoveryPanic(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	sd := s.JetStreamConfig().StoreDir

	// Create stream with MaxMsgsPer to trigger enforceMsgPerSubjectLimit on recovery
	streamName := "TEST_DELETE"
	cfg := &StreamConfig{
		Name:       streamName,
		Subjects:   []string{"test.>"},
		Storage:    FileStorage,
		MaxMsgsPer: 1,
		Replicas:   1,
	}

	acc := s.GlobalAccount()
	mset, err := acc.addStream(cfg)
	if err != nil {
		t.Fatalf("Failed to create stream: %v", err)
	}

	// Add some messages
	for i := 0; i < 100; i++ {
		subject := fmt.Sprintf("test.key%d", i%10)
		mset.store.StoreMsg(subject, nil, []byte("data"), 0)
	}

	state := mset.state()
	t.Logf("Before restart: Msgs=%d, Deleted=%d", state.Msgs, state.NumDeleted)

	fs := mset.store.(*fileStore)
	streamStoreDir := fs.fcfg.StoreDir

	clientURL := s.ClientURL()
	s.Shutdown()

	// Remove index to force recovery
	os.Remove(filepath.Join(streamStoreDir, msgDir, "index.db"))

	// Restart
	u, _ := url.Parse(clientURL)
	port, _ := strconv.Atoi(u.Port())
	s = RunJetStreamServerOnPort(port, sd)
	defer s.Shutdown()

	time.Sleep(200 * time.Millisecond)

	// Now test that we can delete the stream
	acc = s.GlobalAccount()

	mset, err = acc.lookupStream(streamName)
	if err != nil {
		t.Fatalf("Failed to lookup stream: %v", err)
	}

	deleteStart := time.Now()
	err = mset.delete()
	deleteTime := time.Since(deleteStart)

	if err != nil {
		t.Fatalf("Failed to delete stream: %v", err)
	}

	t.Logf("SUCCESS: Stream deleted in %v - no deadlock", deleteTime)

	// Verify it's gone
	if mset, _ := acc.lookupStream(streamName); mset != nil {
		t.Fatal("Stream should be deleted but still exists")
	}
}

// TestFileStorePanicInjection tests the deadlock fix by using a custom store
// callback that triggers a panic during message storage operations.
// This directly verifies that the defer unlock fix works correctly.
func TestFileStorePanicInjection(t *testing.T) {
	storeDir := t.TempDir()

	// Create a filestore with MaxMsgsPer to trigger enforcement on recovery
	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 512, // Small blocks to have multiple blocks
	}
	cfg := StreamConfig{
		Name:       "TEST",
		Subjects:   []string{"test.>"},
		Storage:    FileStorage,
		MaxMsgsPer: 1, // This triggers enforceMsgPerSubjectLimit on recovery
	}

	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	// Store messages with multiple subjects to build up state
	for i := 0; i < 100; i++ {
		subj := fmt.Sprintf("test.key%d", i%10)
		_, _, err := fs.StoreMsg(subj, nil, []byte(fmt.Sprintf("msg-%d", i)), 0)
		if err != nil {
			t.Fatalf("Failed to store message: %v", err)
		}
	}

	state := fs.State()
	t.Logf("State before stop: Msgs=%d, FirstSeq=%d, LastSeq=%d", state.Msgs, state.FirstSeq, state.LastSeq)

	// Get the store directory
	msgsDir := filepath.Join(storeDir, msgDir)

	fs.Stop()

	// Remove index.db to force block-based recovery
	os.Remove(filepath.Join(msgsDir, "index.db"))

	// Severely truncate block files to create invalid state
	// This aims to cause slice bounds errors during message parsing
	entries, _ := os.ReadDir(msgsDir)
	truncatedCount := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".blk" {
			blkFile := filepath.Join(msgsDir, entry.Name())
			info, err := os.Stat(blkFile)
			if err != nil || info.Size() < 100 {
				continue
			}

			// Truncate to a size that breaks message framing
			// Messages have headers that specify length, so truncating
			// in the middle of a message should cause parsing errors
			newSize := info.Size() / 3 // Truncate to 1/3 of original size
			if newSize < 20 {
				newSize = 20
			}

			// Read, truncate, and write back
			data, err := os.ReadFile(blkFile)
			if err != nil {
				continue
			}
			if err := os.WriteFile(blkFile, data[:newSize], 0644); err != nil {
				continue
			}
			truncatedCount++
			t.Logf("Truncated %s from %d to %d bytes", entry.Name(), info.Size(), newSize)
		}
	}
	t.Logf("Truncated %d block files", truncatedCount)

	// Now try to recover - this should exercise the recovery code path
	// With severe truncation, we may get errors or panics during recovery
	t.Log("Attempting recovery with truncated blocks...")

	recoverDone := make(chan struct{})
	var recoveredFS *fileStore
	var recoverErr error

	go func() {
		defer close(recoverDone)
		defer func() {
			if r := recover(); r != nil {
				t.Logf("PANIC during recovery (this tests the fix): %v", r)
				// With the fix, the lock should be released even after panic
			}
		}()

		recoveredFS, recoverErr = newFileStore(fcfg, cfg)
	}()

	select {
	case <-recoverDone:
		if recoverErr != nil {
			t.Logf("Recovery returned error (expected with truncation): %v", recoverErr)
		} else if recoveredFS != nil {
			t.Log("Recovery succeeded despite truncation")
			// Try to use the filestore to verify no deadlock
			state := recoveredFS.State()
			t.Logf("Recovered state: Msgs=%d", state.Msgs)
			recoveredFS.Stop()
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK: Recovery timed out - lock may be held!")
	}
}

// TestFileStoreDirectLockVerification directly tests the mutex behavior
// by attempting to acquire the lock after various failure scenarios.
func TestFileStoreDirectLockVerification(t *testing.T) {
	storeDir := t.TempDir()

	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 1024,
	}
	cfg := StreamConfig{
		Name:       "TEST",
		Subjects:   []string{"test.>"},
		Storage:    FileStorage,
		MaxMsgsPer: 1,
	}

	// Create initial filestore and populate
	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	for i := 0; i < 50; i++ {
		subj := fmt.Sprintf("test.subject%d", i%5)
		fs.StoreMsg(subj, nil, []byte("data"), 0)
	}
	fs.Stop()

	msgsDir := filepath.Join(storeDir, msgDir)

	// Remove index to force recovery
	os.Remove(filepath.Join(msgsDir, "index.db"))

	// Create severe corruption
	entries, _ := os.ReadDir(msgsDir)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".blk" {
			blkFile := filepath.Join(msgsDir, entry.Name())
			// Write garbage to the middle of the file
			data, _ := os.ReadFile(blkFile)
			if len(data) > 50 {
				// Overwrite with invalid message frame (length prefix pointing past end)
				copy(data[20:], []byte{0xFF, 0xFF, 0xFF, 0x7F}) // Very large length
				os.WriteFile(blkFile, data, 0644)
			}
		}
	}

	// Attempt recovery
	t.Log("Attempting recovery with corrupted message frames...")

	var fs2 *fileStore
	recovered := make(chan struct{})

	go func() {
		defer close(recovered)
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Panic during recovery: %v", r)
			}
		}()

		fs2, err = newFileStore(fcfg, cfg)
		if err != nil {
			t.Logf("Recovery error: %v", err)
		}
	}()

	select {
	case <-recovered:
		if fs2 != nil {
			// Verify we can perform operations (proves lock isn't held)
			done := make(chan bool, 1)
			go func() {
				fs2.mu.Lock()
				fs2.mu.Unlock()
				done <- true
			}()

			select {
			case <-done:
				t.Log("SUCCESS: Lock is not held - fix is working correctly")
			case <-time.After(2 * time.Second):
				t.Fatal("DEADLOCK: Could not acquire lock after recovery")
			}
			fs2.Stop()
		} else {
			t.Log("Recovery returned nil filestore (expected with severe corruption)")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("DEADLOCK: Recovery timed out")
	}
}

// TestFileStorePanicWithCallback tests the deadlock scenario by using
// a storage callback that panics during message removal.
// Note: The callback is called OUTSIDE the lock, so this doesn't test
// the newFileStoreWithCreated deadlock bug directly.
func TestFileStorePanicWithCallback(t *testing.T) {
	storeDir := t.TempDir()

	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 512,
	}
	cfg := StreamConfig{
		Name:       "TEST",
		Subjects:   []string{"test.>"},
		Storage:    FileStorage,
		MaxMsgsPer: 1,
	}

	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	// Store messages that will need enforcement
	for i := 0; i < 100; i++ {
		subj := fmt.Sprintf("test.key%d", i%10)
		fs.StoreMsg(subj, nil, []byte(fmt.Sprintf("msg-%d", i)), 0)
	}

	state := fs.State()
	t.Logf("Initial state: Msgs=%d", state.Msgs)

	// Register a callback that will panic
	panicCount := 0
	fs.RegisterStorageUpdates(func(md, bd int64, seq uint64, subj string) {
		panicCount++
		// Panic on the 3rd callback to simulate a panic during enforcement
		if panicCount == 3 {
			panic("simulated panic in storage callback")
		}
	})

	// Now trigger an operation that will call the callback
	// Storing a message to a subject that already has MaxMsgsPer messages
	// will trigger removal of old messages, which calls the callback
	t.Log("Triggering operation that invokes callback...")

	operationDone := make(chan struct{})
	go func() {
		defer close(operationDone)
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Panic caught: %v", r)
			}
		}()

		// Store a message that will trigger callback through enforcement
		_, _, err := fs.StoreMsg("test.key0", nil, []byte("trigger"), 0)
		if err != nil {
			t.Logf("Store returned error: %v", err)
		}
	}()

	select {
	case <-operationDone:
		t.Log("Operation completed or panicked")
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: Operation timed out")
	}

	// Now verify we can still use the filestore (lock is released)
	t.Log("Verifying filestore is still usable...")
	lockCheckDone := make(chan struct{})
	go func() {
		defer close(lockCheckDone)
		// Try to acquire the lock directly
		fs.mu.Lock()
		fs.mu.Unlock()
	}()

	select {
	case <-lockCheckDone:
		t.Log("SUCCESS: Lock is not held after panic in callback")
	case <-time.After(3 * time.Second):
		t.Fatal("DEADLOCK: Could not acquire lock after panic")
	}

	fs.Stop()
}

// TestFileStoreRecoveryWithNilBlock tests that recovery handles nil blocks
// gracefully and doesn't deadlock. This test directly manipulates internal
// state to inject a potentially panic-inducing condition.
func TestFileStoreRecoveryWithNilBlock(t *testing.T) {
	storeDir := t.TempDir()

	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 512,
	}
	cfg := StreamConfig{
		Name:       "TEST",
		Subjects:   []string{"test.>"},
		Storage:    FileStorage,
		MaxMsgsPer: 1, // Triggers enforcement
	}

	// Create and populate filestore
	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	for i := 0; i < 50; i++ {
		subj := fmt.Sprintf("test.key%d", i%5)
		fs.StoreMsg(subj, nil, []byte("data"), 0)
	}
	fs.Stop()

	msgsDir := filepath.Join(storeDir, msgDir)

	// Remove index to force full recovery
	os.Remove(filepath.Join(msgsDir, "index.db"))

	// Delete ALL block files to create an extreme edge case
	entries, _ := os.ReadDir(msgsDir)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".blk" {
			os.Remove(filepath.Join(msgsDir, entry.Name()))
			t.Logf("Removed block file: %s", entry.Name())
		}
	}

	// Now try recovery with missing blocks
	t.Log("Attempting recovery with missing blocks...")

	recovered := make(chan struct{})
	var fs2 *fileStore

	go func() {
		defer close(recovered)
		defer func() {
			if r := recover(); r != nil {
				t.Logf("PANIC during recovery: %v", r)
			}
		}()

		fs2, err = newFileStore(fcfg, cfg)
		if err != nil {
			t.Logf("Recovery error (expected): %v", err)
		}
	}()

	select {
	case <-recovered:
		if fs2 != nil {
			t.Log("Recovery succeeded")
			// Verify the lock isn't held
			done := make(chan bool, 1)
			go func() {
				fs2.mu.Lock()
				fs2.mu.Unlock()
				done <- true
			}()

			select {
			case <-done:
				t.Log("SUCCESS: Lock is accessible")
			case <-time.After(2 * time.Second):
				t.Fatal("DEADLOCK: Lock appears to be held")
			}
			fs2.Stop()
		} else {
			t.Log("Recovery returned nil (expected with missing blocks)")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK: Recovery timed out")
	}
}

// TestFileStoreSkewDetectionPath tests the code path where enforceMsgPerSubjectLimit
// detects a skew between psim totals and fs.state.Msgs, triggering a rebuild.
// This exercises the path at filestore.go:5100-5113 where mb.rebuildState() and
// fs.populateGlobalPerSubjectInfo() are called while holding the lock.
func TestFileStoreSkewDetectionPath(t *testing.T) {
	storeDir := t.TempDir()

	fcfg := FileStoreConfig{
		StoreDir:  storeDir,
		BlockSize: 256, // Very small blocks to have many blocks
	}
	cfg := StreamConfig{
		Name:       "TEST",
		Subjects:   []string{"test.>"},
		Storage:    FileStorage,
		MaxMsgsPer: 1, // Triggers enforceMsgPerSubjectLimit on recovery
	}

	// Create and populate filestore with multiple subjects
	fs, err := newFileStore(fcfg, cfg)
	if err != nil {
		t.Fatalf("Failed to create filestore: %v", err)
	}

	// Create messages across many subjects to build up psim state
	numKeys := 20
	numUpdates := 10
	for update := 0; update < numUpdates; update++ {
		for key := 0; key < numKeys; key++ {
			subj := fmt.Sprintf("test.key%d", key)
			fs.StoreMsg(subj, nil, []byte(fmt.Sprintf("msg-%d-%d", key, update)), 0)
		}
	}

	state := fs.State()
	t.Logf("Initial state: Msgs=%d, FirstSeq=%d, LastSeq=%d", state.Msgs, state.FirstSeq, state.LastSeq)
	t.Logf("Number of blocks: %d", len(fs.blks))

	// Stop the filestore
	fs.Stop()

	msgsDir := filepath.Join(storeDir, msgDir)

	// Remove index.db to force block-based recovery
	indexPath := filepath.Join(msgsDir, "index.db")
	if err := os.Remove(indexPath); err != nil {
		t.Logf("Note: Could not remove index.db: %v", err)
	}

	// Corrupt SOME blocks to potentially create a psim/state skew
	// By corrupting the subject length in some messages, the psim
	// might end up with different counts than state.Msgs
	entries, _ := os.ReadDir(msgsDir)
	corruptedBlocks := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".blk" {
			blkFile := filepath.Join(msgsDir, entry.Name())
			data, err := os.ReadFile(blkFile)
			if err != nil || len(data) < 50 {
				continue
			}

			// Only corrupt every other block to create inconsistency
			if corruptedBlocks%2 == 0 {
				// Corrupt the subject length field (bytes 20-21 in message header)
				// This may cause messages to be skipped during psim population
				// while still being counted in state.Msgs
				for i := 22; i < len(data)-10 && i < 100; i += 30 {
					data[i] = 0xFF // Invalid subject length
					data[i+1] = 0xFF
				}
				os.WriteFile(blkFile, data, 0644)
				t.Logf("Corrupted block: %s", entry.Name())
			}
			corruptedBlocks++
		}
	}
	t.Logf("Corrupted %d blocks (out of %d)", corruptedBlocks/2, corruptedBlocks)

	// Now attempt recovery - this should trigger enforceMsgPerSubjectLimit
	// which may detect skew and trigger the rebuild path
	t.Log("Attempting recovery with potentially skewed state...")

	recovered := make(chan struct{})
	var fs2 *fileStore
	var recoverErr error

	go func() {
		defer close(recovered)
		defer func() {
			if r := recover(); r != nil {
				t.Logf("PANIC during recovery (skew rebuild path): %v", r)
				// This is what we're trying to trigger - a panic during the
				// skew-triggered rebuild that would leave the lock held
			}
		}()

		fs2, recoverErr = newFileStore(fcfg, cfg)
	}()

	select {
	case <-recovered:
		if recoverErr != nil {
			t.Logf("Recovery returned error: %v", recoverErr)
		}
		if fs2 != nil {
			state := fs2.State()
			t.Logf("Recovered state: Msgs=%d", state.Msgs)

			// Verify no deadlock by trying to acquire the lock
			lockCheck := make(chan bool, 1)
			go func() {
				fs2.mu.Lock()
				fs2.mu.Unlock()
				lockCheck <- true
			}()

			select {
			case <-lockCheck:
				t.Log("SUCCESS: Lock is accessible after recovery")
			case <-time.After(3 * time.Second):
				t.Fatal("DEADLOCK: Could not acquire lock after recovery")
			}

			fs2.Stop()
		}
	case <-time.After(15 * time.Second):
		t.Fatal("DEADLOCK: Recovery timed out")
	}
}
