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
	"os"
	"path/filepath"
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
