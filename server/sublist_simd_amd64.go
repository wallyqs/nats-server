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

//go:build goexperiment.simd && amd64

package server

import (
	"math/bits"
	"simd/archsimd"
	"strings"
	"unsafe"
)

// tokenizeSubjectIntoSlice splits the subject into tokens at '.' delimiters
// using SIMD acceleration. Use similar to append — the updated slice is returned.
func tokenizeSubjectIntoSlice(tts []string, subject string) []string {
	if len(subject) < 16 {
		return tokenizeSubjectIntoSliceScalar(tts, subject)
	}

	data := stringToBytes(subject)
	base := unsafe.Pointer(&data[0])
	dot := archsimd.BroadcastInt8x16(int8(btsep))
	last := 0
	n := len(data)
	loopEnd := n - n%16

	for i := 0; i < loopEnd; i += 16 {
		chunk := (*[16]int8)(unsafe.Add(base, i))
		v := archsimd.LoadInt8x16(chunk)
		b := v.Equal(dot).ToBits()
		for b != 0 {
			j := bits.TrailingZeros16(b)
			pos := i + j
			tts = append(tts, subject[last:pos])
			last = pos + 1
			b &= b - 1
		}
	}

	// Scalar tail for remaining bytes.
	for i := loopEnd; i < n; i++ {
		if data[i] == btsep {
			tts = append(tts, subject[last:i])
			last = i + 1
		}
	}
	tts = append(tts, subject[last:])
	return tts
}

// subjectIsLiteral checks whether the subject contains no wildcard tokens
// using SIMD acceleration. Returns true if the subject is a literal (no wildcards).
func subjectIsLiteral(subject string) bool {
	if len(subject) < 16 {
		return subjectIsLiteralScalar(subject)
	}

	data := stringToBytes(subject)
	base := unsafe.Pointer(&data[0])
	star := archsimd.BroadcastInt8x16(int8(pwc))
	gt := archsimd.BroadcastInt8x16(int8(fwc))
	n := len(data)
	loopEnd := n - n%16

	for i := 0; i < loopEnd; i += 16 {
		chunk := (*[16]int8)(unsafe.Add(base, i))
		v := archsimd.LoadInt8x16(chunk)
		starMask := v.Equal(star).ToBits()
		gtMask := v.Equal(gt).ToBits()
		combined := starMask | gtMask
		for combined != 0 {
			j := bits.TrailingZeros16(combined)
			pos := i + j
			// Validate token boundary: must be at start/end of subject or surrounded by '.'.
			if (pos == 0 || data[pos-1] == btsep) &&
				(pos+1 == n || data[pos+1] == btsep) {
				return false
			}
			combined &= combined - 1
		}
	}

	// Scalar tail.
	for i := loopEnd; i < n; i++ {
		c := data[i]
		if c == pwc || c == fwc {
			if (i == 0 || data[i-1] == btsep) &&
				(i+1 == n || data[i+1] == btsep) {
				return false
			}
		}
	}
	return true
}

// subjectIsValid checks whether a subject has valid structure using SIMD
// acceleration: non-empty, no empty tokens (consecutive/leading/trailing dots),
// no whitespace characters, and '>' wildcard only as the last token.
func subjectIsValid(subject string) bool {
	n := len(subject)
	if n == 0 {
		return false
	}

	data := stringToBytes(subject)

	// Quick boundary checks: leading or trailing dot means an empty token.
	if data[0] == btsep || data[n-1] == btsep {
		return false
	}

	if n < 16 {
		return subjectIsValidScalar(subject)
	}

	base := unsafe.Pointer(&data[0])

	// Broadcast comparison vectors.
	dotV := archsimd.BroadcastInt8x16(int8(btsep))
	spaceV := archsimd.BroadcastInt8x16(int8(' '))
	tabV := archsimd.BroadcastInt8x16(int8('\t'))
	nlV := archsimd.BroadcastInt8x16(int8('\n'))
	ffV := archsimd.BroadcastInt8x16(int8('\f'))
	crV := archsimd.BroadcastInt8x16(int8('\r'))
	gtV := archsimd.BroadcastInt8x16(int8(fwc))

	loopEnd := n - n%16

	for i := 0; i < loopEnd; i += 16 {
		chunk := (*[16]int8)(unsafe.Add(base, i))
		v := archsimd.LoadInt8x16(chunk)

		// Any whitespace character in the subject is immediately invalid.
		wsMask := v.Equal(spaceV).ToBits() |
			v.Equal(tabV).ToBits() |
			v.Equal(nlV).ToBits() |
			v.Equal(ffV).ToBits() |
			v.Equal(crV).ToBits()
		if wsMask != 0 {
			return false
		}

		// Check for consecutive dots (empty tokens).
		dotMask := v.Equal(dotV).ToBits()
		if dotMask != 0 {
			// Two adjacent dot bits within this 16-byte chunk.
			if dotMask&(dotMask>>1) != 0 {
				return false
			}
			// Cross-chunk boundary: first byte of this chunk is a dot and
			// last byte of the previous chunk was also a dot.
			if i > 0 && dotMask&1 != 0 && data[i-1] == btsep {
				return false
			}
		}

		// '>' as a standalone token must be the last token.
		gtMask := v.Equal(gtV).ToBits()
		for gtMask != 0 {
			j := bits.TrailingZeros16(gtMask)
			pos := i + j
			if (pos == 0 || data[pos-1] == btsep) &&
				(pos+1 == n || data[pos+1] == btsep) {
				// It is a '>' token — only valid at the very end.
				if pos+1 < n {
					return false
				}
			}
			gtMask &= gtMask - 1
		}
	}

	// Scalar tail for remaining bytes.
	for i := loopEnd; i < n; i++ {
		c := data[i]
		switch c {
		case ' ', '\t', '\n', '\r', '\f':
			return false
		case btsep:
			if data[i-1] == btsep {
				return false
			}
		case fwc:
			if (i == 0 || data[i-1] == btsep) && (i+1 == n || data[i+1] == btsep) {
				if i+1 < n {
					return false
				}
			}
		}
	}

	return true
}

// subjectHasWildcard checks whether the subject contains any wildcard tokens
// using SIMD acceleration. Returns true if the subject has wildcards.
func subjectHasWildcard(subject string) bool {
	if len(subject) < 16 {
		return subjectHasWildcardScalar(subject)
	}

	data := stringToBytes(subject)
	base := unsafe.Pointer(&data[0])
	star := archsimd.BroadcastInt8x16(int8(pwc))
	gt := archsimd.BroadcastInt8x16(int8(fwc))
	n := len(data)
	loopEnd := n - n%16

	for i := 0; i < loopEnd; i += 16 {
		chunk := (*[16]int8)(unsafe.Add(base, i))
		v := archsimd.LoadInt8x16(chunk)
		starMask := v.Equal(star).ToBits()
		gtMask := v.Equal(gt).ToBits()
		combined := starMask | gtMask
		for combined != 0 {
			j := bits.TrailingZeros16(combined)
			pos := i + j
			if (pos == 0 || data[pos-1] == btsep) &&
				(pos+1 == n || data[pos+1] == btsep) {
				return true
			}
			combined &= combined - 1
		}
	}

	// Scalar tail.
	for i := loopEnd; i < n; i++ {
		c := data[i]
		if c == pwc || c == fwc {
			if (i == 0 || data[i-1] == btsep) &&
				(i+1 == n || data[i+1] == btsep) {
				return true
			}
		}
	}
	return false
}

// subjectIsSubsetMatch checks whether subject is a subset match of test,
// using SIMD to compare prefix bytes from both strings simultaneously.
// Instead of tokenizing both strings into []string slices, this loads
// 16 bytes from each string into SIMD registers and compares them directly.
// Identical chunks (same bytes, same dot positions, no wildcards) are
// skipped at 16 bytes per iteration. On the first mismatch the remainder
// is handled by a scalar streaming loop that walks tokens incrementally.
func subjectIsSubsetMatch(subject, test string) bool {
	ns, nt := len(subject), len(test)
	if ns == 0 || nt == 0 {
		return false
	}

	si, ti := 0, 0

	// SIMD fast path: compare 16-byte chunks from both strings.
	// When all 16 bytes are identical (same content and dot positions),
	// advance both pointers by 16 — skipping multiple tokens at once.
	if ns >= 16 && nt >= 16 {
		sData := stringToBytes(subject)
		tData := stringToBytes(test)
		sBase := unsafe.Pointer(&sData[0])
		tBase := unsafe.Pointer(&tData[0])
		dot := archsimd.BroadcastInt8x16(int8(btsep))

		for si+16 <= ns && ti+16 <= nt {
			vs := archsimd.LoadInt8x16((*[16]int8)(unsafe.Add(sBase, si)))
			vt := archsimd.LoadInt8x16((*[16]int8)(unsafe.Add(tBase, ti)))
			eqBits := vs.Equal(vt).ToBits()
			if eqBits != 0xFFFF {
				// Mismatch within this chunk. Skip past the last dot
				// in the matching prefix so the scalar tail begins at
				// a clean token boundary.
				diffPos := bits.TrailingZeros16(^eqBits)
				if diffPos > 0 {
					dotBits := vs.Equal(dot).ToBits() & uint16(1<<diffPos-1)
					if dotBits != 0 {
						adv := 15 - bits.LeadingZeros16(dotBits) + 1
						si += adv
						ti += adv
					}
				}
				break
			}
			si += 16
			ti += 16
		}
	}

	// Scalar streaming: walk both remainders token-by-token.
	return subjectIsSubsetMatchStreamFrom(subject[si:], test[ti:])
}

// subjectIsSubsetMatchStreamFrom performs the subset match comparison by
// walking both strings token-by-token, finding dot separators incrementally
// rather than pre-tokenizing into []string slices.
func subjectIsSubsetMatchStreamFrom(subject, test string) bool {
	for {
		if len(test) == 0 {
			return len(subject) == 0
		}
		if len(subject) == 0 {
			return false
		}

		// Next token from test (the filter/pattern).
		ti := strings.IndexByte(test, btsep)
		var t2 string
		if ti >= 0 {
			t2, test = test[:ti], test[ti+1:]
		} else {
			t2, test = test, _EMPTY_
		}
		if len(t2) == 0 {
			return false
		}
		if len(t2) == 1 && t2[0] == fwc {
			return true
		}

		// Next token from subject.
		si := strings.IndexByte(subject, btsep)
		var t1 string
		if si >= 0 {
			t1, subject = subject[:si], subject[si+1:]
		} else {
			t1, subject = subject, _EMPTY_
		}
		if len(t1) == 0 || (len(t1) == 1 && t1[0] == fwc) {
			return false
		}

		if len(t1) == 1 && t1[0] == pwc {
			if !(len(t2) == 1 && t2[0] == pwc) {
				return false
			}
			continue
		}
		if t2[0] != pwc && t1 != t2 {
			return false
		}
	}
}
