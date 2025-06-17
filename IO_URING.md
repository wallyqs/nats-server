# IO_URING Support in NATS Server

This implementation adds **Linux io_uring** support to the NATS filestore for improved async I/O performance.

## Overview

io_uring provides asynchronous I/O capabilities on Linux (kernel 5.1+) that can significantly improve performance for high-throughput JetStream file operations by:

- Reducing system call overhead through batching
- Enabling true async file I/O without blocking goroutines  
- Minimizing context switches between user and kernel space

## Usage

### Configuration

To enable io_uring in your filestore configuration:

```go
fcfg := FileStoreConfig{
    StoreDir:          "/path/to/storage",
    UseIOUring:        true,                // Enable io_uring
    IOUringQueueDepth: 64,                  // Optional: queue depth (default: 64)
    // ... other config options
}

fs, err := newFileStore(fcfg, streamConfig)
```

### Platform Support

- **Linux with io_uring support**: Uses async io_uring operations
- **Other platforms**: Gracefully falls back to standard synchronous I/O
- **Linux without io_uring**: Auto-detects and falls back to sync I/O

### Build Tags

The implementation uses build tags to conditionally compile io_uring support:

- Build with io_uring: `go build -tags iouring`
- Build without io_uring: `go build` (default)

On non-Linux platforms, io_uring is automatically disabled regardless of build tags.

## Implementation Details

### Architecture

The implementation provides:

1. **Platform abstraction**: `IOUring` interface with Linux-specific and stub implementations
2. **Graceful fallback**: Auto-detection and fallback to sync I/O when io_uring unavailable
3. **Performance preservation**: Existing performance characteristics maintained on fallback

### Key Components

- `filestore_iouring.go`: Linux io_uring implementation (build tag: `linux && iouring`)
- `filestore_iouring_stub.go`: Fallback implementation (build tag: `!linux || !iouring`)
- Modified `loadBlock()` and `writeAt()` functions to use async I/O

### Integration Points

The io_uring integration enhances these file operations:

- **Message block reads**: `loadBlock()` function uses async `pread`
- **Message block writes**: `writeAt()` function uses async `pwrite`
- **Message erasure**: `eraseMsg()` function uses async `pwrite`

## Testing

Run the io_uring-specific test:

```bash
go test -v ./server -run TestFileStoreIOUring
```

On Linux with io_uring support:
```bash
go test -v -tags iouring ./server -run TestFileStoreIOUring
```

## Performance Characteristics

### Expected Benefits

- **High-throughput scenarios**: Reduced CPU usage and improved throughput
- **Concurrent access**: Better performance with multiple concurrent streams
- **Large message processing**: More efficient handling of large message blocks

### Fallback Behavior

When io_uring is not available:
- No performance degradation compared to standard NATS
- Transparent fallback to existing sync I/O implementation
- All existing functionality preserved

## Requirements

- Linux kernel 5.1+ (for io_uring support)
- `github.com/iceber/iouring-go` dependency (automatically managed)

## Notes

- io_uring operations are currently limited to file I/O (not network I/O)
- Queue depth can be tuned based on workload characteristics
- All existing JetStream features and configurations remain compatible