# User Testing

Testing surface, validation approach, and resource cost classification.

---

## Validation Surface

This is a pure Go library with no interactive UI, CLI, or API surface.

**Primary surface:** `go test ./conf/v2/... -v` output analysis
**Tool:** Direct `go test` execution and output inspection
**Assertions validated by:** Running specific test cases that exercise the public API contracts

## Validation Concurrency

- `go test` is lightweight (~300 MB peak per invocation)
- Machine: 8 cores, 16 GB RAM, ~6 GB baseline usage
- Usable headroom: ~10 GB * 0.7 = 7 GB
- Each validator uses ~300 MB
- **Max concurrent validators: 5**
