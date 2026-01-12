// Copyright 2016-2025 The NATS Authors
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

package gsl

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/internal/antithesis"
	"github.com/nats-io/nats-server/v2/server/stree"
)

func TestGenericSublistInit(t *testing.T) {
	s := NewSublist[struct{}]()
	require_Equal(t, s.count, 0)
	require_Equal(t, s.Count(), s.count)
}

func TestGenericSublistInsertCount(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert("foo", struct{}{}))
	require_NoError(t, s.Insert("bar", struct{}{}))
	require_NoError(t, s.Insert("foo.bar", struct{}{}))
	require_Equal(t, s.Count(), 3)
}

func TestGenericSublistSimple(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert("foo", struct{}{}))
	require_Matches(t, s, "foo", 1)
}

func TestGenericSublistSimpleMultiTokens(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert("foo.bar.baz", struct{}{}))
	require_Matches(t, s, "foo.bar.baz", 1)
}

func TestGenericSublistPartialWildcard(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert("a.b.c", struct{}{}))
	require_NoError(t, s.Insert("a.*.c", struct{}{}))
	require_Matches(t, s, "a.b.c", 2)
}

func TestGenericSublistPartialWildcardAtEnd(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert("a.b.c", struct{}{}))
	require_NoError(t, s.Insert("a.b.*", struct{}{}))
	require_Matches(t, s, "a.b.c", 2)
}

func TestGenericSublistFullWildcard(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert("a.b.c", struct{}{}))
	require_NoError(t, s.Insert("a.>", struct{}{}))
	require_Matches(t, s, "a.b.c", 2)
	require_Matches(t, s, "a.>", 1)
}

func TestGenericSublistRemove(t *testing.T) {
	s := NewSublist[struct{}]()

	require_NoError(t, s.Insert("a.b.c.d", struct{}{}))
	require_Equal(t, s.Count(), 1)
	require_Matches(t, s, "a.b.c.d", 1)

	require_NoError(t, s.Remove("a.b.c.d", struct{}{}))
	require_Equal(t, s.Count(), 0)
	require_Matches(t, s, "a.b.c.d", 0)
}

func TestGenericSublistRemoveWildcard(t *testing.T) {
	s := NewSublist[int]()

	require_NoError(t, s.Insert("a.b.c.d", 11))
	require_NoError(t, s.Insert("a.b.*.d", 22))
	require_NoError(t, s.Insert("a.b.>", 33))
	require_Equal(t, s.Count(), 3)
	require_Matches(t, s, "a.b.c.d", 3)

	require_NoError(t, s.Remove("a.b.*.d", 22))
	require_Equal(t, s.Count(), 2)
	require_Matches(t, s, "a.b.c.d", 2)

	require_NoError(t, s.Remove("a.b.>", 33))
	require_Equal(t, s.Count(), 1)
	require_Matches(t, s, "a.b.c.d", 1)

	require_NoError(t, s.Remove("a.b.c.d", 11))
	require_Equal(t, s.Count(), 0)
	require_Matches(t, s, "a.b.c.d", 0)
}

func TestGenericSublistRemoveCleanup(t *testing.T) {
	s := NewSublist[struct{}]()
	require_Equal(t, s.numLevels(), 0)
	require_NoError(t, s.Insert("a.b.c.d.e.f", struct{}{}))
	require_Equal(t, s.numLevels(), 6)
	require_NoError(t, s.Remove("a.b.c.d.e.f", struct{}{}))
	require_Equal(t, s.numLevels(), 0)
}

func TestGenericSublistRemoveCleanupWildcards(t *testing.T) {
	s := NewSublist[struct{}]()
	require_Equal(t, s.numLevels(), 0)
	require_NoError(t, s.Insert("a.b.*.d.e.>", struct{}{}))
	require_Equal(t, s.numLevels(), 6)
	require_NoError(t, s.Remove("a.b.*.d.e.>", struct{}{}))
	require_Equal(t, s.numLevels(), 0)
}

func TestGenericSublistInvalidSubjectsInsert(t *testing.T) {
	s := NewSublist[struct{}]()
	// Insert, or subscriptions, can have wildcards, but not empty tokens,
	// and can not have a FWC that is not the terminal token.
	require_Error(t, s.Insert(".foo", struct{}{}), ErrInvalidSubject)
	require_Error(t, s.Insert("foo.", struct{}{}), ErrInvalidSubject)
	require_Error(t, s.Insert("foo..bar", struct{}{}), ErrInvalidSubject)
	require_Error(t, s.Insert("foo.bar..baz", struct{}{}), ErrInvalidSubject)
	require_Error(t, s.Insert("foo.>.baz", struct{}{}), ErrInvalidSubject)
}

func TestGenericSublistBadSubjectOnRemove(t *testing.T) {
	s := NewSublist[struct{}]()
	require_Error(t, s.Insert("a.b..d", struct{}{}), ErrInvalidSubject)
	require_Error(t, s.Remove("a.b..d", struct{}{}), ErrInvalidSubject)
	require_Error(t, s.Remove("a.>.b", struct{}{}), ErrInvalidSubject)
}

func TestGenericSublistTwoTokenPubMatchSingleTokenSub(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert("foo", struct{}{}))
	require_Matches(t, s, "foo", 1)
	require_Matches(t, s, "foo.bar", 0)
}

func TestGenericSublistInsertWithWildcardsAsLiterals(t *testing.T) {
	s := NewSublist[int]()
	for i, subject := range []string{"foo.*-", "foo.>-"} {
		require_NoError(t, s.Insert(subject, i))
		require_Matches(t, s, "foo.bar", 0)
		require_Matches(t, s, subject, 1)
	}
}

func TestGenericSublistRemoveWithWildcardsAsLiterals(t *testing.T) {
	s := NewSublist[int]()
	for i, subject := range []string{"foo.*-", "foo.>-"} {
		require_NoError(t, s.Insert(subject, i))
		require_Matches(t, s, "foo.bar", 0)
		require_Matches(t, s, subject, 1)
		require_Error(t, s.Remove("foo.bar", i), ErrNotFound)
		require_Equal(t, s.Count(), 1)
		require_NoError(t, s.Remove(subject, i))
		require_Equal(t, s.Count(), 0)
	}
}

func TestGenericSublistMatchWithEmptyTokens(t *testing.T) {
	s := NewSublist[struct{}]()
	require_NoError(t, s.Insert(">", struct{}{}))
	for _, subject := range []string{".foo", "..foo", "foo..", "foo.", "foo..bar", "foo...bar"} {
		t.Run(subject, func(t *testing.T) {
			require_Matches(t, s, subject, 0)
		})
	}
}

func TestGenericSublistHasInterest(t *testing.T) {
	s := NewSublist[int]()
	require_NoError(t, s.Insert("foo", 11))

	// Expect to find that "foo" matches but "bar" doesn't.
	// At this point nothing should be in the cache.
	require_True(t, s.HasInterest("foo"))
	require_False(t, s.HasInterest("bar"))

	// Call Match on a subject we know there is no match.
	require_Matches(t, s, "bar", 0)
	require_False(t, s.HasInterest("bar"))

	// Remove fooSub and check interest again
	require_NoError(t, s.Remove("foo", 11))
	require_False(t, s.HasInterest("foo"))

	// Try with some wildcards
	require_NoError(t, s.Insert("foo.*", 22))
	require_False(t, s.HasInterest("foo"))
	require_True(t, s.HasInterest("foo.bar"))
	require_False(t, s.HasInterest("foo.bar.baz"))

	// Remove sub, there should be no interest
	require_NoError(t, s.Remove("foo.*", 22))
	require_False(t, s.HasInterest("foo"))
	require_False(t, s.HasInterest("foo.bar"))
	require_False(t, s.HasInterest("foo.bar.baz"))

	require_NoError(t, s.Insert("foo.>", 33))
	require_False(t, s.HasInterest("foo"))
	require_True(t, s.HasInterest("foo.bar"))
	require_True(t, s.HasInterest("foo.bar.baz"))

	require_NoError(t, s.Remove("foo.>", 33))
	require_False(t, s.HasInterest("foo"))
	require_False(t, s.HasInterest("foo.bar"))
	require_False(t, s.HasInterest("foo.bar.baz"))

	require_NoError(t, s.Insert("*.>", 44))
	require_False(t, s.HasInterest("foo"))
	require_True(t, s.HasInterest("foo.bar"))
	require_True(t, s.HasInterest("foo.baz"))
	require_NoError(t, s.Remove("*.>", 44))

	require_NoError(t, s.Insert("*.bar", 55))
	require_False(t, s.HasInterest("foo"))
	require_True(t, s.HasInterest("foo.bar"))
	require_False(t, s.HasInterest("foo.baz"))
	require_NoError(t, s.Remove("*.bar", 55))

	require_NoError(t, s.Insert("*", 66))
	require_True(t, s.HasInterest("foo"))
	require_False(t, s.HasInterest("foo.bar"))
	require_NoError(t, s.Remove("*", 66))
}

func TestGenericSublistNumInterest(t *testing.T) {
	s := NewSublist[int]()
	require_NoError(t, s.Insert("foo", 11))

	require_NumInterest := func(t *testing.T, subj string, wnp int) {
		t.Helper()
		require_Matches(t, s, subj, wnp)
		require_Equal(t, s.NumInterest(subj), wnp)
	}

	// Expect to find that "foo" matches but "bar" doesn't.
	// At this point nothing should be in the cache.
	require_NumInterest(t, "foo", 1)
	require_NumInterest(t, "bar", 0)

	// Remove fooSub and check interest again
	require_NoError(t, s.Remove("foo", 11))
	require_NumInterest(t, "foo", 0)

	// Try with some wildcards
	require_NoError(t, s.Insert("foo.*", 22))
	require_NumInterest(t, "foo", 0)
	require_NumInterest(t, "foo.bar", 1)
	require_NumInterest(t, "foo.bar.baz", 0)

	// Remove sub, there should be no interest
	require_NoError(t, s.Remove("foo.*", 22))
	require_NumInterest(t, "foo", 0)
	require_NumInterest(t, "foo.bar", 0)
	require_NumInterest(t, "foo.bar.baz", 0)

	require_NoError(t, s.Insert("foo.>", 33))
	require_NumInterest(t, "foo", 0)
	require_NumInterest(t, "foo.bar", 1)
	require_NumInterest(t, "foo.bar.baz", 1)

	require_NoError(t, s.Remove("foo.>", 33))
	require_NumInterest(t, "foo", 0)
	require_NumInterest(t, "foo.bar", 0)
	require_NumInterest(t, "foo.bar.baz", 0)

	require_NoError(t, s.Insert("*.>", 44))
	require_NumInterest(t, "foo", 0)
	require_NumInterest(t, "foo.bar", 1)
	require_NumInterest(t, "foo.bar.baz", 1)
	require_NoError(t, s.Remove("*.>", 44))

	require_NoError(t, s.Insert("*.bar", 55))
	require_NumInterest(t, "foo", 0)
	require_NumInterest(t, "foo.bar", 1)
	require_NumInterest(t, "foo.bar.baz", 0)
	require_NoError(t, s.Remove("*.bar", 55))

	require_NoError(t, s.Insert("*", 66))
	require_NumInterest(t, "foo", 1)
	require_NumInterest(t, "foo.bar", 0)
	require_NoError(t, s.Remove("*", 66))
}

func TestGenericSublistInterestBasedIntersection(t *testing.T) {
	st := stree.NewSubjectTree[struct{}]()
	st.Insert([]byte("one.two.three.four"), struct{}{})
	st.Insert([]byte("one.two.three.five"), struct{}{})
	st.Insert([]byte("one.two.six"), struct{}{})
	st.Insert([]byte("one.two.seven"), struct{}{})
	st.Insert([]byte("eight.nine"), struct{}{})
	st.Insert([]byte("stream.A"), struct{}{})
	st.Insert([]byte("stream.A.child"), struct{}{})

	require_NoDuplicates := func(t *testing.T, got map[string]int) {
		t.Helper()
		for _, c := range got {
			require_Equal(t, c, 1)
		}
	}

	t.Run("Literals", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("one.two.six", 11))
		require_NoError(t, sl.Insert("eight.nine", 22))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	t.Run("PWC", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("one.two.*.*", 11))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	t.Run("PWCOverlapping", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("one.two.*.four", 11))
		require_NoError(t, sl.Insert("one.two.*.*", 22))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	t.Run("PWCAll", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("*.*", 11))
		require_NoError(t, sl.Insert("*.*.*", 22))
		require_NoError(t, sl.Insert("*.*.*.*", 33))
		require_True(t, sl.HasInterest("foo.bar"))
		require_True(t, sl.HasInterest("foo.bar.baz"))
		require_True(t, sl.HasInterest("foo.bar.baz.qux"))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 7)
		require_NoDuplicates(t, got)
	})

	t.Run("FWC", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("one.>", 11))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 4)
		require_NoDuplicates(t, got)
	})

	t.Run("FWCOverlapping", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("one.two.three.four", 11))
		require_NoError(t, sl.Insert("one.>", 22))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 4)
		require_NoDuplicates(t, got)
	})

	t.Run("FWCExtended", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("stream.A.>", 11))
		require_NoError(t, sl.Insert("stream.A", 22))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	t.Run("PWCExtended", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("stream.*.child", 11))
		require_NoError(t, sl.Insert("stream.A", 22))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	t.Run("PWCExtendedAggressive", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("stream.A.child", 11))
		require_NoError(t, sl.Insert("*.A.child", 22))
		require_NoError(t, sl.Insert("stream.*.child", 22))
		require_NoError(t, sl.Insert("stream.A.*", 22))
		require_NoError(t, sl.Insert("stream.*.*", 22))
		require_NoError(t, sl.Insert("*.A.*", 22))
		require_NoError(t, sl.Insert("*.*.child", 22))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 1)
		require_NoDuplicates(t, got)
	})

	t.Run("FWCAll", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert(">", 11))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 7)
		require_NoDuplicates(t, got)
	})

	t.Run("NoMatch", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("one", 11))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 0)
	})

	t.Run("NoMatches", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("one", 11))
		require_NoError(t, sl.Insert("eight", 22))
		require_NoError(t, sl.Insert("ten", 33))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 0)
	})

	t.Run("NoMatchPartial", func(t *testing.T) {
		got := map[string]int{}
		sl := NewSublist[int]()
		require_NoError(t, sl.Insert("stream.A.not-child", 11))
		require_NoError(t, sl.Insert("stream.A.child.>", 22))
		IntersectStree(st, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})
		require_Len(t, len(got), 0)
		require_NoDuplicates(t, got)
	})

	// Regression test for issue where mixed wildcard and literal filters
	// with different leaf nodes would skip the literal path incorrectly.
	t.Run("MixedWildcardLiteralDifferentLeaves", func(t *testing.T) {
		// Create a stree with a subject that only matches via the literal path
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("events.literal.other"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// Wildcard pattern: events.*.something
		require_NoError(t, sl.Insert("events.*.something", 11))
		// Literal pattern: events.literal.other
		require_NoError(t, sl.Insert("events.literal.other", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// The literal path should NOT be skipped because it leads to "other"
		// while the wildcard path leads to "something" (different leaves)
		require_Len(t, len(got), 1)
		require_Equal(t, got["events.literal.other"], 1)
		require_NoDuplicates(t, got)
	})

	// Regression test for deep path divergence where paths share intermediate
	// nodes but diverge at deeper levels.
	t.Run("DeepPathDivergence", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("a.x.b.other"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// Wildcard pattern: a.*.b.something
		require_NoError(t, sl.Insert("a.*.b.something", 11))
		// Literal pattern: a.x.b.other
		require_NoError(t, sl.Insert("a.x.b.other", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// Both paths share "b" at level 2, but diverge at level 3:
		// wildcard leads to "something", literal leads to "other"
		// The literal path should NOT be skipped.
		require_Len(t, len(got), 1)
		require_Equal(t, got["a.x.b.other"], 1)
		require_NoDuplicates(t, got)
	})

	// Regression test for multiple PWCs with different leaves.
	t.Run("MultiplePWCDifferentLeaves", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("a.x.b.y.d"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// Wildcard pattern with two PWCs: a.*.b.*.c
		require_NoError(t, sl.Insert("a.*.b.*.c", 11))
		// Literal pattern with PWC: a.x.b.*.d (different leaf!)
		require_NoError(t, sl.Insert("a.x.b.*.d", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// a.*.b.*.c does NOT match a.x.b.y.d (c != d)
		// a.x.b.*.d DOES match a.x.b.y.d
		require_Len(t, len(got), 1)
		require_Equal(t, got["a.x.b.y.d"], 1)
		require_NoDuplicates(t, got)
	})

	// FWC regression tests - ensure full wildcard works correctly with
	// mixed patterns.

	// Test FWC at same level as a literal
	t.Run("FWCWithLiteralSameLevel", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("events.foo.bar"), struct{}{})
		localSt.Insert([]byte("events.foo.baz"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// FWC pattern
		require_NoError(t, sl.Insert("events.>", 11))
		// Literal pattern
		require_NoError(t, sl.Insert("events.foo.bar", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// Both subjects should be found (events.> matches both)
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	// Test FWC at same level as PWC
	t.Run("FWCWithPWCSameLevel", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("events.foo.bar"), struct{}{})
		localSt.Insert([]byte("events.foo.baz.qux"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// FWC pattern
		require_NoError(t, sl.Insert("events.>", 11))
		// PWC pattern
		require_NoError(t, sl.Insert("events.*.bar", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// Both subjects should be found
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	// Test FWC deeper in tree with literal sibling
	t.Run("FWCDeeperLevelWithLiteral", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("a.b.c.d.e"), struct{}{})
		localSt.Insert([]byte("a.b.x.y.z"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// FWC pattern starting at level 2
		require_NoError(t, sl.Insert("a.b.>", 11))
		// Literal pattern
		require_NoError(t, sl.Insert("a.b.c.d.e", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// Both subjects should be found
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	// Test mixed FWC, PWC and literal - complex case
	t.Run("MixedFWCPWCLiteral", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("events.foo.bar.specific"), struct{}{})
		localSt.Insert([]byte("events.foo.other.data"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// PWC pattern
		require_NoError(t, sl.Insert("events.*.bar.something", 11))
		// Literal with FWC at end
		require_NoError(t, sl.Insert("events.foo.>", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// events.foo.> should match both subjects
		require_Len(t, len(got), 2)
		require_NoDuplicates(t, got)
	})

	// Test FWC and literal in completely different paths
	t.Run("FWCAndLiteralDifferentPaths", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("events.foo.bar"), struct{}{})
		localSt.Insert([]byte("metrics.cpu.usage"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// FWC pattern on events
		require_NoError(t, sl.Insert("events.>", 11))
		// Literal pattern on metrics
		require_NoError(t, sl.Insert("metrics.cpu.usage", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// Both subjects should be found via different paths
		require_Len(t, len(got), 2)
		require_Equal(t, got["events.foo.bar"], 1)
		require_Equal(t, got["metrics.cpu.usage"], 1)
		require_NoDuplicates(t, got)
	})

	// Test PWC and FWC in different branches of the tree
	t.Run("PWCAndFWCDifferentBranches", func(t *testing.T) {
		localSt := stree.NewSubjectTree[struct{}]()
		localSt.Insert([]byte("a.x.specific.data"), struct{}{})
		localSt.Insert([]byte("a.y.other.stuff"), struct{}{})

		got := map[string]int{}
		sl := NewSublist[int]()
		// PWC pattern: a.*.specific.something (doesn't match "data")
		require_NoError(t, sl.Insert("a.*.specific.something", 11))
		// FWC pattern on different branch: a.y.>
		require_NoError(t, sl.Insert("a.y.>", 22))

		IntersectStree(localSt, sl, func(subj []byte, entry *struct{}) {
			got[string(subj)]++
		})

		// a.*.specific.something does NOT match a.x.specific.data (something != data)
		// a.y.> DOES match a.y.other.stuff
		require_Len(t, len(got), 1)
		require_Equal(t, got["a.y.other.stuff"], 1)
		require_NoDuplicates(t, got)
	})
}

// --- BENCHMARKS ---

// BenchmarkIntersectStreeNonOverlapping benchmarks the common case where
// filter patterns don't overlap (uses optimized tree walk).
func BenchmarkIntersectStreeNonOverlapping(b *testing.B) {
	for _, numSubjects := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("subjects=%d", numSubjects), func(b *testing.B) {
			// Create stree with many subjects
			st := stree.NewSubjectTree[int]()
			for i := 0; i < numSubjects; i++ {
				subj := fmt.Sprintf("orders.region%d.user%d.created", i%10, i)
				st.Insert([]byte(subj), i)
			}

			// Non-overlapping patterns (different prefixes)
			sl := NewSublist[int]()
			sl.Insert("orders.region0.>", 1)
			sl.Insert("orders.region5.>", 2)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				count := 0
				IntersectStree(st, sl, func(subj []byte, entry *int) {
					count++
				})
			}
		})
	}
}

// BenchmarkIntersectStreeOverlapping benchmarks the case where filter patterns
// overlap (falls back to linear scan in hybrid approach).
func BenchmarkIntersectStreeOverlapping(b *testing.B) {
	for _, numSubjects := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("subjects=%d", numSubjects), func(b *testing.B) {
			// Create stree with many subjects
			st := stree.NewSubjectTree[int]()
			for i := 0; i < numSubjects; i++ {
				subj := fmt.Sprintf("events.user%d.action%d", i%100, i%10)
				st.Insert([]byte(subj), i)
			}

			// Overlapping patterns (PWC and literal at same level)
			sl := NewSublist[int]()
			sl.Insert("events.*.action0", 1)
			sl.Insert("events.user0.action5", 2)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				count := 0
				IntersectStree(st, sl, func(subj []byte, entry *int) {
					count++
				})
			}
		})
	}
}

// BenchmarkIntersectStreeSelectivePattern benchmarks selective patterns
// where tree walk can prune many branches.
func BenchmarkIntersectStreeSelectivePattern(b *testing.B) {
	for _, numSubjects := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("subjects=%d", numSubjects), func(b *testing.B) {
			// Create stree with subjects across many branches
			st := stree.NewSubjectTree[int]()
			for i := 0; i < numSubjects; i++ {
				// Spread across 100 different first-level prefixes
				subj := fmt.Sprintf("prefix%d.sub%d.data", i%100, i)
				st.Insert([]byte(subj), i)
			}

			// Very selective pattern - only matches 1% of subjects
			sl := NewSublist[int]()
			sl.Insert("prefix0.>", 1)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				count := 0
				IntersectStree(st, sl, func(subj []byte, entry *int) {
					count++
				})
			}
		})
	}
}

// BenchmarkIntersectStreeBroadPattern benchmarks broad patterns
// that match most subjects.
func BenchmarkIntersectStreeBroadPattern(b *testing.B) {
	for _, numSubjects := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("subjects=%d", numSubjects), func(b *testing.B) {
			st := stree.NewSubjectTree[int]()
			for i := 0; i < numSubjects; i++ {
				subj := fmt.Sprintf("events.user%d.action", i)
				st.Insert([]byte(subj), i)
			}

			// Broad pattern - matches everything
			sl := NewSublist[int]()
			sl.Insert(">", 1)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				count := 0
				IntersectStree(st, sl, func(subj []byte, entry *int) {
					count++
				})
			}
		})
	}
}

// BenchmarkIntersectStreeConcurrent benchmarks concurrent IntersectStree calls
// to measure lock contention (or lack thereof with atomics).
func BenchmarkIntersectStreeConcurrent(b *testing.B) {
	// Create shared stree
	st := stree.NewSubjectTree[int]()
	for i := 0; i < 1000; i++ {
		subj := fmt.Sprintf("events.user%d.action%d", i%100, i%10)
		st.Insert([]byte(subj), i)
	}

	// Create shared sublist
	sl := NewSublist[int]()
	sl.Insert("events.*.action0", 1)
	sl.Insert("events.user0.>", 2)

	b.Run("goroutines=1", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			count := 0
			IntersectStree(st, sl, func(subj []byte, entry *int) {
				count++
			})
		}
	})

	b.Run("goroutines=4", func(b *testing.B) {
		b.SetParallelism(4)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				count := 0
				IntersectStree(st, sl, func(subj []byte, entry *int) {
					count++
				})
			}
		})
	})

	b.Run("goroutines=16", func(b *testing.B) {
		b.SetParallelism(16)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				count := 0
				IntersectStree(st, sl, func(subj []byte, entry *int) {
					count++
				})
			}
		})
	})
}

// --- TEST HELPERS ---

func require_Matches[T comparable](t *testing.T, s *GenericSublist[T], sub string, c int) {
	t.Helper()
	matches := 0
	s.Match(sub, func(_ T) {
		matches++
	})
	require_Equal(t, matches, c)
}

func require_True(t testing.TB, b bool) {
	t.Helper()
	if !b {
		antithesis.AssertUnreachable(t, "Failed require_True check", nil)
		t.Fatalf("require true, but got false")
	}
}

func require_False(t testing.TB, b bool) {
	t.Helper()
	if b {
		antithesis.AssertUnreachable(t, "Failed require_False check", nil)
		t.Fatalf("require false, but got true")
	}
}

func require_NoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		antithesis.AssertUnreachable(t, "Failed require_NoError check", map[string]any{
			"error": err.Error(),
		})
		t.Fatalf("require no error, but got: %v", err)
	}
}

func require_Error(t testing.TB, err error, expected ...error) {
	t.Helper()
	if err == nil {
		antithesis.AssertUnreachable(t, "Failed require_Error check (nil error)", nil)
		t.Fatalf("require error, but got none")
	}
	if len(expected) == 0 {
		return
	}
	// Try to strip nats prefix from Go library if present.
	const natsErrPre = "nats: "
	eStr := err.Error()
	if strings.HasPrefix(eStr, natsErrPre) {
		eStr = strings.Replace(eStr, natsErrPre, _EMPTY_, 1)
	}

	for _, e := range expected {
		if err == e || strings.Contains(eStr, e.Error()) || strings.Contains(e.Error(), eStr) {
			return
		}
	}

	antithesis.AssertUnreachable(t, "Failed require_Error check (unexpected error)", map[string]any{
		"error": err.Error(),
	})
	t.Fatalf("Expected one of %v, got '%v'", expected, err)
}

func require_Equal[T comparable](t testing.TB, a, b T) {
	t.Helper()
	if a != b {
		antithesis.AssertUnreachable(t, "Failed require_Equal check", nil)
		t.Fatalf("require %T equal, but got: %v != %v", a, a, b)
	}
}

func require_Len(t testing.TB, a, b int) {
	t.Helper()
	if a != b {
		antithesis.AssertUnreachable(t, "Failed require_Len check", nil)
		t.Fatalf("require len, but got: %v != %v", a, b)
	}
}
