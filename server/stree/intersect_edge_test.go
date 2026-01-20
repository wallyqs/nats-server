package stree

import (
	"testing"

	"github.com/nats-io/nats-server/v2/server/gsl"
)

// TestIntersectGSLCallbackSliceLifetime tests the documented behavior that the callback
// receives a slice that shares a backing array which is reused across callbacks.
// Callers must copy the slice if they need to store it beyond the callback.
func TestIntersectGSLCallbackSliceLifetime(t *testing.T) {
	st := NewSubjectTree[int]()
	st.Insert([]byte("foo.bar"), 1)
	st.Insert([]byte("foo.baz"), 2)
	st.Insert([]byte("foo.qux"), 3)

	sl := gsl.NewSublist[int]()
	sl.Insert("foo.*", 1)

	// CORRECT PATTERN: Convert to string immediately in callback
	found := make(map[string]bool)
	IntersectGSL(st, sl, func(subj []byte, val *int) {
		// Convert to string inside callback - this is the correct pattern
		found[string(subj)] = true
	})

	// Verify we got all 3 subjects
	if len(found) != 3 {
		t.Fatalf("expected 3 subjects, got %d", len(found))
	}
	if !found["foo.bar"] {
		t.Error("missing foo.bar")
	}
	if !found["foo.baz"] {
		t.Error("missing foo.baz")
	}
	if !found["foo.qux"] {
		t.Error("missing foo.qux")
	}
}

// TestIntersectGSLCallbackSliceSharing verifies that storing the raw slice
// directly (without copying) results in shared backing array corruption.
// This documents the behavior for future reference.
func TestIntersectGSLCallbackSliceSharing(t *testing.T) {
	st := NewSubjectTree[int]()
	st.Insert([]byte("foo.bar"), 1)
	st.Insert([]byte("foo.baz"), 2)
	st.Insert([]byte("foo.qux"), 3)

	sl := gsl.NewSublist[int]()
	sl.Insert("foo.*", 1)

	// INCORRECT PATTERN: Storing raw slices shares backing array
	var rawSlices [][]byte
	IntersectGSL(st, sl, func(subj []byte, val *int) {
		rawSlices = append(rawSlices, subj)
	})

	// Verify we got 3 callbacks
	if len(rawSlices) != 3 {
		t.Fatalf("expected 3 subjects, got %d", len(rawSlices))
	}

	// Due to shared backing array, all slices now contain the same content
	// (the last subject that was processed)
	unique := make(map[string]bool)
	for _, s := range rawSlices {
		unique[string(s)] = true
	}

	// Only 1 unique value because all slices share same backing array
	if len(unique) != 1 {
		t.Errorf("expected 1 unique value due to shared backing array, got %d", len(unique))
	}
}

// TestIntersectGSLLongSubject tests subjects longer than 256 bytes
// which exceed the stack-allocated buffer.
func TestIntersectGSLLongSubject(t *testing.T) {
	st := NewSubjectTree[int]()
	
	// Create a subject longer than 256 bytes
	longSubj := make([]byte, 0, 300)
	for i := 0; i < 50; i++ {
		if i > 0 {
			longSubj = append(longSubj, '.')
		}
		longSubj = append(longSubj, "token"...)
	}
	// This should be about 50*5 + 49 = 299 bytes
	
	st.Insert(longSubj, 1)
	
	sl := gsl.NewSublist[int]()
	sl.Insert(">", 1)  // Match everything
	
	var found bool
	IntersectGSL(st, sl, func(subj []byte, val *int) {
		if string(subj) == string(longSubj) {
			found = true
		}
	})
	
	if !found {
		t.Error("long subject not found")
	}
}

// TestIntersectGSLEmptySublist tests with an empty sublist
func TestIntersectGSLEmptySublist(t *testing.T) {
	st := NewSubjectTree[int]()
	st.Insert([]byte("foo.bar"), 1)
	
	sl := gsl.NewSublist[int]()
	// Empty sublist - no patterns
	
	count := 0
	IntersectGSL(st, sl, func(subj []byte, val *int) {
		count++
	})
	
	if count != 0 {
		t.Errorf("expected 0 matches with empty sublist, got %d", count)
	}
}

// TestIntersectGSLEmptyTree tests with an empty tree
func TestIntersectGSLEmptyTree(t *testing.T) {
	st := NewSubjectTree[int]()
	// Empty tree
	
	sl := gsl.NewSublist[int]()
	sl.Insert(">", 1)
	
	count := 0
	IntersectGSL(st, sl, func(subj []byte, val *int) {
		count++
	})
	
	if count != 0 {
		t.Errorf("expected 0 matches with empty tree, got %d", count)
	}
}

// TestIntersectGSLNilInputs tests nil inputs
func TestIntersectGSLNilInputs(t *testing.T) {
	st := NewSubjectTree[int]()
	st.Insert([]byte("foo"), 1)
	sl := gsl.NewSublist[int]()
	sl.Insert("foo", 1)

	// Should not panic with nil tree
	IntersectGSL[int, int](nil, sl, func(subj []byte, val *int) {
		t.Error("callback should not be called with nil tree")
	})

	// Should not panic with nil sublist
	IntersectGSL[int, int](st, nil, func(subj []byte, val *int) {
		t.Error("callback should not be called with nil sublist")
	})
}

// TestIntersectGSLSingleTokenSubject tests single token subjects
func TestIntersectGSLSingleTokenSubject(t *testing.T) {
	st := NewSubjectTree[int]()
	st.Insert([]byte("foo"), 1)
	st.Insert([]byte("bar"), 2)
	
	sl := gsl.NewSublist[int]()
	sl.Insert("foo", 1)
	
	var found []string
	IntersectGSL(st, sl, func(subj []byte, val *int) {
		found = append(found, string(subj))
	})
	
	if len(found) != 1 || found[0] != "foo" {
		t.Errorf("expected [foo], got %v", found)
	}
}
