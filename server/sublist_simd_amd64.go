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
	"unsafe"
)

// numTokens returns the number of tokens in the subject using SIMD acceleration.
// Subjects shorter than 16 bytes fall back to the scalar implementation.
func numTokens(subject string) int {
	if len(subject) == 0 {
		return 0
	}
	if len(subject) < 16 {
		return numTokensScalar(subject)
	}

	data := stringToBytes(subject)
	base := unsafe.Pointer(&data[0])
	dot := archsimd.BroadcastInt8x16(int8('.'))
	count := 0
	n := len(data)
	loopEnd := n - n%16

	for i := 0; i < loopEnd; i += 16 {
		chunk := (*[16]int8)(unsafe.Add(base, i))
		v := archsimd.LoadInt8x16(chunk)
		b := v.Equal(dot).ToBits()
		count += bits.OnesCount16(b)
	}

	// Scalar tail for remaining bytes.
	for i := loopEnd; i < n; i++ {
		if data[i] == '.' {
			count++
		}
	}
	return count + 1
}

// tokenizeSubjectIntoSlice splits the subject into tokens at '.' delimiters
// using SIMD acceleration. Use similar to append — the updated slice is returned.
func tokenizeSubjectIntoSlice(tts []string, subject string) []string {
	if len(subject) < 16 {
		return tokenizeSubjectIntoSliceScalar(tts, subject)
	}

	data := stringToBytes(subject)
	base := unsafe.Pointer(&data[0])
	dot := archsimd.BroadcastInt8x16(int8('.'))
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
		if data[i] == '.' {
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
	star := archsimd.BroadcastInt8x16(int8('*'))
	gt := archsimd.BroadcastInt8x16(int8('>'))
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
			if (pos == 0 || data[pos-1] == '.') &&
				(pos+1 == n || data[pos+1] == '.') {
				return false
			}
			combined &= combined - 1
		}
	}

	// Scalar tail.
	for i := loopEnd; i < n; i++ {
		c := data[i]
		if c == '*' || c == '>' {
			if (i == 0 || data[i-1] == '.') &&
				(i+1 == n || data[i+1] == '.') {
				return false
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
	star := archsimd.BroadcastInt8x16(int8('*'))
	gt := archsimd.BroadcastInt8x16(int8('>'))
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
			if (pos == 0 || data[pos-1] == '.') &&
				(pos+1 == n || data[pos+1] == '.') {
				return true
			}
			combined &= combined - 1
		}
	}

	// Scalar tail.
	for i := loopEnd; i < n; i++ {
		c := data[i]
		if c == '*' || c == '>' {
			if (i == 0 || data[i-1] == '.') &&
				(i+1 == n || data[i+1] == '.') {
				return true
			}
		}
	}
	return false
}
