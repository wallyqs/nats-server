# NATS Server Release Summaries

## v2.11.12 (January 27, 2025)

A large release focused on JetStream stability and performance.

### New Features
- WebSocket-specific ping interval configuration (`ping_internal` in websocket block)
- TLS certificate expiry info (`tls_cert_not_after`) added to `varz` monitoring endpoint

### Performance Improvements
- Subject-filtered source setup scanning is considerably faster
- Consumer interest checks on large gap streams are significantly faster
- Consumer file store creation no longer contends on stream lock
- Eliminated duplicate sorting in filter subject recalculation

### Bug Fixes

**JetStream (30+ fixes):**
- Fixed stream desyncs during snapshotting
- Fixed gateway-originated acknowledgement reply subject transforms
- Resolved data races in stream health checks and mirror consumers
- Corrected subject intersection logic affecting consumer calculations
- Fixed filestore compaction issues and cache errors
- Resolved Raft membership and quorum counting issues

**MQTT (6 fixes):**
- Maximum payload size now correctly enforced
- QoS2 message retrieval after restart fixed
- Retained message corruption in clusters resolved
- Retained messages now work correctly when sourced from different accounts with subject transforms

**General:**
- WebSocket decompression buffer limiting
- Configuration parser self-reference detection
- Header handling corruption prevention

### Dependencies
- Go 1.25.6, nats.go v1.48.0, nkeys v0.4.12, klauspost/compress v1.18.3

---

## v2.11.14 (March 9, 2026)

A security-focused release addressing two CVEs.

### Security Fixes
- **CVE-2026-29785:** Crash when receiving a leafnode subscription before compression negotiation completes (affects systems with leafnode compression enabled)
- **CVE-2026-27889:** Multiple WebSocket parsing issues that could cause panics (affects systems with WebSockets enabled)

### Bug Fixes

**Leafnodes:**
- Receiving a leafnode subscription before negotiating compression no longer causes server crashes

**WebSockets:**
- Corrected 64-bit payload length parsing to prevent panics
- Reject compressed frames when compression wasn't negotiated
- Improved Origin header validation (protocol scheme, host, port)
- Better handling of failed connection upgrades
- Proper validation of CLOSE frame lengths and status codes
- Proper compressor state reset during max payload errors
- Eliminated panics from empty compressed buffers

### Dependencies
- Go 1.25.8, golang.org/x/crypto v0.48.0, golang.org/x/sys v0.42.0
