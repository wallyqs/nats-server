# Stack Allocation & Escape Analysis Review

Reviewed: `parser.go`, `client.go`, `sublist.go`, `route.go`, `gateway.go`, `leafnode.go`

The codebase already has excellent allocation discipline -- stack-backed arrays
(`[32]string{}`, `[512]byte{}`, etc.), manual arg splitting instead of
`strings.Split`, `bytesToString` zero-copy conversions, and scratch buffers on
`parseState`. The findings below are the remaining opportunities, ordered by
hot-path impact.

## CRITICAL -- Message Publish/Delivery Pipeline (every message)

### 1. `updateStats` closure in `processMsgResults` (client.go:5014-5049)

The closure captures 7+ variables (`dlvMsgs`, `dlvExtraSize`, `dlvRouteMsgs`,
`dlvLeafMsgs`, `prodIsMQTT`, `acc`, `c`, `msg`), forcing all of them to
heap-escape. This fires on every published message.

**Fix:** Inline the ~20 line body at its two call sites (lines 5397, 5479).
Eliminates the closure object + heap escape of all captured locals.

### 2. Repeated `string(c.pa.subject)` in `processInboundClientMsg` (client.go:4198, 4279, 4289, 4302)

Up to 4 separate `string([]byte)` conversions of the same subject per message.
The compiler optimizes map lookups (`m[string(b)]`) but not other uses.

**Fix:** Convert once at function entry: `subjectStr := string(c.pa.subject)`
and reuse. Net savings: 2-3 allocations per message.

### 3. `bytes.IndexFunc` closure in `processMsgResults` (client.go:4978-4986)

The anonymous function captures `counter` by reference, heap-allocating both the
closure and the counter variable. Fires on every routed JetStream message.

**Fix:** Replace with a hand-written byte loop:

```go
counter, li := 0, -1
for i, b := range creply {
    if b == '.' {
        counter++
    } else if b == '@' && counter >= 8 {
        li = i
        break
    }
}
```

### 4. `SublistResult` heap allocation on cache miss (sublist.go:582)

`result := &SublistResult{}` allocates on every subject cache miss across all
client types. The code already has a `FIXME(dlc)` here.

**Fix:** Use `sync.Pool` for `SublistResult` objects, resetting slice lengths
but keeping backing arrays.

## HIGH -- Per-subscriber delivery path

### 5. `string(subject)` and `string(reply)` in `deliverMsg` internal callbacks (client.go:3797, 3803)

Two string allocations per internal subscription delivery (JetStream consumers,
service imports, system events).

**Fix:** Use the existing `bytesToString` zero-copy conversion. Requires audit
that callbacks don't retain the strings beyond the call.

### 6. `string(subject)` in `checkDenySub` from `deliverMsg` (client.go:3646)

Allocates a string for permission checking on every delivery to clients with
deny permissions.

**Fix:** Change `checkDenySub` to accept `[]byte` and use `m[string(b)]`
directly in the map lookup (compiler-optimized to not allocate).

### 7. `[]byte(qname)` in `addNodeToResults` (sublist.go:722)

Called from `matchLevel`, the innermost trie descent loop. Converts string map
key to `[]byte` for `findQSlot` comparison.

**Fix:** Change `findQSlot` to accept `string` and compare with
`bytesToString(qr[0].queue)`. Zero allocations.

### 8. `strconv.Itoa` in `msgHeader` / `msgHeaderForRouteOrLeaf` (client.go:3531, 3572)

Allocates intermediate string in per-message header construction when stripping
headers.

**Fix:** `mh = strconv.AppendInt(mh, int64(c.pa.size-c.pa.hdr), 10)` --
appends directly into the existing buffer.

### 9. `[]byte(constString)` conversions of prefix constants (client.go:3662, 3664, 4699, 4973)

`[]byte(gwReplyPrefix)`, `[]byte(oldGWReplyPrefix)`, `[]byte(jsAckPre)`
allocate on each call.

**Fix:** Pre-allocate package-level `var gwReplyPrefixB = []byte(gwReplyPrefix)`
etc. (pattern already used for `jsDirectGetPreB`).

## MEDIUM -- Subscribe/Unsubscribe & Routing paths

### 10. `splitArg` returns slice backed by local array (client.go:2882-2903)

The returned slice forces the local `[MAX_MSG_ARGS][]byte{}` array to
heap-escape. The codebase already solved this in `processPub`/`processHeaderPub`
by inlining the split logic with a comment explaining why.

**Fix:** Inline `splitArg` in `parseSub` and `processUnsub`, matching the
existing pattern in `processPub`.

### 11. `parseSub` copies argument buffer (client.go:2907-2909)

`arg := make([]byte, len(argo))` always heap-allocates. Has existing
`FIXME(dlc)` comment.

**Fix:** Stack-backed buffer: `var _arg [256]byte; arg := _arg[:len(argo)]`
with fallback to `make` for oversized args.

### 12. `fnv.New32a()` interface boxing in `computeRoutePoolIdx` (route.go:539-540)

Interface allocation + `[]byte(string)` conversion on every route pool index
computation.

**Fix:** Inline FNV-1a arithmetic operating directly on string bytes:

```go
h := uint32(2166136261)
for i := 0; i < len(an); i++ {
    h ^= uint32(an[i])
    h *= 16777619
}
```

### 13. `string(c.pa.subject)` in `processInboundLeafMsg` (leafnode.go:3092)

Unconditional string conversion defeats the compiler's map-lookup optimization
on cache hits.

**Fix:** Use `bytesToString` for lookup, real `string()` only on cache-miss
insertion.

### 14. Double `string(sub.queue)` in `removeFromNode` (sublist.go:1010, 1017)

Map lookup is compiler-optimized but `delete(m, string(b))` is not.

**Fix:** Convert once: `qname := string(sub.queue)`, reuse for both lookup and
delete.

### 15. `strings.SplitSeq` in `Insert()` and `remove()` (sublist.go:366, 859)

Iterator overhead vs. the manual tokenization loop already used in `match()`.

**Fix:** Use the same manual `for i := 0; i < len(subject); i++` tokenization
with `[32]string{}` stack array.

### 16. Multiple `[]byte(to)` in `processServiceImport` (client.go:4740, 4795, 4852-4855)

Same string converted to `[]byte` up to 4 times in one function.

**Fix:** Convert once: `toBytes := []byte(to)` and reuse.

### 17. `bytes.Split` for token counting (client.go:2983)

Allocates a `[][]byte` just to check `len() > MaxSubTokens`.

**Fix:** Count dots inline:
`for _, b := range sub.subject { if b == '.' { n++ } }`

## LOWER -- Error/trace paths & less frequent operations

### 18. `traceOp` allocates `[]any{}` slice (client.go:2075)

When tracing is enabled, every operation allocates a heap slice + interface
boxes.

**Fix:** `var opa [2]any` stack array, slice to `opa[:n]`.

### 19. `setHeader` uses `http.Header` map + `bytes.Buffer` + dual `strconv.Itoa` (client.go:4470-4500)

4+ allocations per service import header manipulation.

**Fix:** Direct `bb.WriteString(key + ": " + value + "\r\n")` instead of
`http.Header.Write()`. Use `strconv.AppendInt` instead of `Itoa`.

### 20. `fmt.Sprintf` in MAPPING trace (parser.go:502)

**Fix:** Stack buffer with `append`.

### 21. `&resp{}` heap allocation for reply tracking (client.go:3878)

**Fix:** Change `replies` map from `map[string]*resp` to `map[string]resp` (24
bytes, fine as value type).

### 22. `getHeader()` creates 3 wrapper objects (parser.go:1298-1310)

`bytes.NewReader` + `bufio.NewReader` + `textproto.NewReader` per header parse.

**Fix:** Custom header parser operating directly on `[]byte` without reader
wrappers.

### 23. Gateway `string()` conversions in `gatewayHandleSubjectNoInterest` (gateway.go:2848-2864)

Map assignment keys not optimized by compiler.

**Fix:** Convert `accName`/`subject` once at scope entry, reuse.

### 24. `[]byte(to)` / `[]byte(rt.sub.im.to)` in `msgHeaderForRouteOrLeaf` (client.go:3491, 3493)

**Fix:** Use `mh = append(mh, stringValue...)` instead of converting to
`[]byte` first.

## Summary by estimated impact

| Tier | Findings | Savings estimate |
|------|----------|-----------------|
| **Every message** | #1 closure, #2 repeated string conv, #3 IndexFunc closure, #4 SublistResult | 3-7 allocs/msg |
| **Per subscriber** | #5-#9 delivery path conversions | 1-4 allocs/delivery |
| **Sub/Unsub** | #10-#11 splitArg + parseSub copy | 2 allocs/sub |
| **Routing** | #12-#16 route/leaf/gateway paths | 1-3 allocs/routed msg |
| **Lower freq** | #17-#24 trace/error/headers | Varies |

The highest-ROI changes are **#1** (inline `updateStats` closure), **#2**
(deduplicate subject string conversion), **#3** (replace `IndexFunc` closure),
**#4** (`sync.Pool` for `SublistResult`), and **#10** (inline `splitArg`). These
are all mechanical refactors with minimal risk.
