# SIMD Subject Tokenization Benchmark Results

**CPU:** Intel Xeon Platinum 8581C @ 2.10GHz
**Go:** go1.27-devel (gotip) with `GOEXPERIMENT=simd`
**OS:** Linux amd64
**Runs:** 6 per benchmark

## benchstat: Scalar vs SIMD (GOEXPERIMENT=simd)

```
                                │   Scalar     │              SIMD                  │
                                │    sec/op    │   sec/op     vs base               │
SIMDNumTokens/short_10B-16         7.604n ± 2%   5.186n ± 2%  -31.81% (p=0.002 n=6)
SIMDNumTokens/medium_50B-16        27.98n ± 3%   12.84n ± 2%  -54.12% (p=0.002 n=6)
SIMDNumTokens/long_120B-16         37.80n ± 1%   10.26n ± 1%  -72.84% (p=0.002 n=6)
SIMDNumTokens/verylong_250-16      58.23n ± 4%   15.43n ± 1%  -73.49% (p=0.002 n=6)
SIMDTokenize/short_10B-16          17.62n ± 3%   13.54n ± 4%  -23.16% (p=0.002 n=6)
SIMDTokenize/medium_50B-16         92.84n ± 3%   28.05n ± 3%  -69.79% (p=0.002 n=6)
SIMDTokenize/long_120B-16         154.30n ± 4%   35.72n ± 2%  -76.85% (p=0.002 n=6)
SIMDTokenize/verylong_250-16      276.60n ± 3%   53.79n ± 2%  -80.55% (p=0.002 n=6)
SIMDIsLiteral/short_10B-16         11.04n ± 1%   11.69n ± 4%   +5.79% (p=0.002 n=6)
SIMDIsLiteral/medium_50B-16        56.20n ± 1%   15.98n ± 2%  -71.56% (p=0.002 n=6)
SIMDIsLiteral/long_120B-16         94.07n ± 2%   14.57n ± 4%  -84.51% (p=0.002 n=6)
SIMDIsLiteral/verylong_250-16     181.35n ± 2%   22.48n ± 2%  -87.60% (p=0.002 n=6)
SIMDHasWildcard/short_10B-16       11.05n ± 4%   12.06n ± 2%   +9.09% (p=0.002 n=6)
SIMDHasWildcard/medium_50B-16      55.34n ± 1%   15.67n ± 3%  -71.70% (p=0.002 n=6)
SIMDHasWildcard/long_120B-16       91.30n ± 1%   14.39n ± 1%  -84.24% (p=0.002 n=6)
SIMDHasWildcard/verylong_250-16   174.65n ± 3%   22.27n ± 1%  -87.25% (p=0.002 n=6)
geomean                            52.55n        16.44n       -68.71%
```

## Summary by Function

### numTokens (count '.' delimiters)
| Subject Size | Scalar | SIMD | Speedup |
|---|---|---|---|
| 10B (short) | 7.60 ns | 5.19 ns | **1.5x** |
| 50B (medium) | 27.98 ns | 12.84 ns | **2.2x** |
| 120B (long) | 37.80 ns | 10.26 ns | **3.7x** |
| 250B (very long) | 58.23 ns | 15.43 ns | **3.8x** |

### tokenizeSubjectIntoSlice (split at '.' into tokens)
| Subject Size | Scalar | SIMD | Speedup |
|---|---|---|---|
| 10B (short) | 17.62 ns | 13.54 ns | **1.3x** |
| 50B (medium) | 92.84 ns | 28.05 ns | **3.3x** |
| 120B (long) | 154.30 ns | 35.72 ns | **4.3x** |
| 250B (very long) | 276.60 ns | 53.79 ns | **5.1x** |

### subjectIsLiteral (check for wildcard tokens)
| Subject Size | Scalar | SIMD | Speedup |
|---|---|---|---|
| 10B (short) | 11.04 ns | 11.69 ns | 0.94x (regression) |
| 50B (medium) | 56.20 ns | 15.98 ns | **3.5x** |
| 120B (long) | 94.07 ns | 14.57 ns | **6.5x** |
| 250B (very long) | 181.35 ns | 22.48 ns | **8.1x** |

### subjectHasWildcard (check for wildcard tokens)
| Subject Size | Scalar | SIMD | Speedup |
|---|---|---|---|
| 10B (short) | 11.05 ns | 12.06 ns | 0.92x (regression) |
| 50B (medium) | 55.34 ns | 15.67 ns | **3.5x** |
| 120B (long) | 91.30 ns | 14.39 ns | **6.3x** |
| 250B (very long) | 174.65 ns | 22.27 ns | **7.8x** |

## Correctness

All 58 SIMD correctness tests pass (TestSIMDNumTokens, TestSIMDTokenizeSubjectIntoSlice, TestSIMDSubjectIsLiteral, TestSIMDSubjectHasWildcard). Each test verifies that the SIMD implementation produces identical results to the scalar implementation across edge cases including empty strings, boundary lengths (exactly 16 bytes, 17 bytes), leading/trailing dots, embedded wildcards, and very long subjects.

## Key Observations

1. **Geometric mean speedup: 3.2x** (68.71% reduction in time) across all benchmarks.
2. **Largest gains on longer subjects**: The SIMD advantage scales with subject length, reaching **5-8x speedup** on 120-250 byte subjects for wildcard checking and tokenization.
3. **Short subject overhead**: For subjects under 16 bytes, `subjectIsLiteral` and `subjectHasWildcard` show a ~5-9% regression due to the SIMD function call overhead before falling back to scalar. The `numTokens` SIMD path still wins at short lengths because it uses a different strategy.
4. **Zero allocations**: Both SIMD and scalar paths are allocation-free across all benchmarks.
5. **tokenizeSubjectIntoSlice** shows the most consistent gains, from 1.3x on short to 5.1x on very long subjects, which is significant since this is one of the hottest paths in NATS subject routing.
