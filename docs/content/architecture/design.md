---
title: "Design Overview"
weight: 1
---

# Design Overview

Celeris architecture and design principles.

## Architecture Layers

Celeris is built in three distinct layers:

```
┌─────────────────────────────────────────┐
│     Application Layer (pkg/celeris)     │
│  • Router                                │
│  • Handlers                              │
│  • Middleware                            │
│  • Context                               │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│   Protocol Layer (internal/frame,       │
│                   internal/stream)       │
│  • HTTP/2 Frame Parsing                  │
│  • Stream Management                     │
│  • Flow Control                          │
│  • HPACK Compression                     │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│   Transport Layer (internal/transport)  │
│  • gnet Event Loop                       │
│  • TCP Connection Management             │
│  • Buffer Management                     │
└─────────────────────────────────────────┘
```

## Transport Layer

### gnet Integration

The transport layer uses [gnet](https://github.com/panjf2000/gnet), a high-performance event-driven networking framework:

**Key Components:**
- `Server`: Implements gnet.EventHandler
- `Connection`: Wraps gnet.Conn
- Event handlers: OnBoot, OnOpen, OnClose, OnTraffic

**Benefits:**
- Zero-copy buffer management
- Multi-core support
- SO_REUSEPORT for load balancing
- Edge-triggered I/O

### Connection Lifecycle

```
Client Connect
      ↓
  OnOpen() - Create Connection object
      ↓
  OnTraffic() - Read HTTP/2 preface
      ↓
  Send SETTINGS frame
      ↓
  OnTraffic() - Process HTTP/2 frames
      ↓
  OnClose() - Cleanup
```

## Protocol Layer

### Frame Parsing

The `internal/frame` package handles HTTP/2 frame encoding/decoding:

**Frame Types Supported:**
- DATA
- HEADERS
- SETTINGS
- WINDOW_UPDATE
- RST_STREAM
- PRIORITY

**HPACK Compression:**
- Header encoding with `hpack.Encoder`
- Header decoding with `hpack.Decoder`
- Dynamic table management

### Stream Management

The `internal/stream` package manages HTTP/2 streams:

**Stream States:**
```
idle → open → half-closed → closed
```

**Features:**
- Stream multiplexing
- Flow control (per-stream and connection-level)
- Priority handling
- Concurrent stream processing

**Key Components:**
- `Stream`: Represents a single HTTP/2 stream
- `Manager`: Manages multiple streams
- `Processor`: Processes incoming frames

## Application Layer

### Router

High-performance trie-based router:

**Data Structure:**
```
        /
       ╱ ╲
    users  api
     ╱      ╲
   :id      v1
           ╱  ╲
       users  posts
```

**Features:**
- O(k) lookup time (k = path segments)
- Parameter extraction
- Wildcard matching
- Route groups

### Context

Request/response abstraction:

**Design Principles:**
- Single object for request & response
- Zero-copy where possible
- Chainable API
- Type-safe parameter access

### Middleware

Function wrapping pattern:

```go
type Middleware func(Handler) Handler
```

**Execution Order:**
```
Request → MW1 → MW2 → MW3 → Handler → MW3 → MW2 → MW1 → Response
```

## HTTP/2 Specifics

### Connection Preface

```
1. Client sends: "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
2. Client sends: SETTINGS frame
3. Server sends: SETTINGS frame
4. Both send: SETTINGS ACK
5. Ready for streams
```

### Flow Control

**Window Management:**
- Initial window: 65535 bytes
- Per-stream windows
- Connection-level window
- Automatic WINDOW_UPDATE

**Algorithm:**
```
1. Receive DATA frame
2. Process data
3. Send WINDOW_UPDATE
4. Update local window
```

### Frame Priority

HTTP/2 streams can have priorities:
- Weight (1-256)
- Dependencies
- Exclusive flag

*Note: Currently, Celeris processes streams in order of arrival. Priority handling is planned.*

## Performance Optimizations

### Zero-Copy

- gnet provides zero-copy buffer access
- Reuse Context objects
- Buffer pools for allocations

### Connection Pooling

Clients should reuse HTTP/2 connections:
- One connection per host
- Multiplexed streams
- Reduced overhead

### Goroutine Management

- Stream processing in goroutines
- Worker pool for handlers
- Bounded concurrency

## Thread Safety

**Safe:**
- Context within a single request
- Stream within its goroutine
- Router after initialization

**Unsafe (requires locking):**
- Shared application state
- Connection writing (handled internally)
- Stream manager operations (handled internally)

## Error Handling

**Error Propagation:**
```
Handler error → Middleware → Response
                     ↓
               RST_STREAM (if needed)
```

**Error Codes:**
- Application errors: HTTP status codes
- Protocol errors: RST_STREAM with error code
- Connection errors: GOAWAY frame

## Future Enhancements

Planned improvements:

1. **Server Push**
   - Push resources to clients
   - Link header parsing

2. **Priority Handling**
   - Implement stream dependencies
   - Weight-based scheduling

3. **Compression**
   - gzip/deflate middleware
   - Content negotiation

4. **Graceful Shutdown**
   - GOAWAY frame
   - Drain existing streams
   - Reject new streams

5. **Metrics & Tracing**
   - Prometheus metrics
   - OpenTelemetry support
   - Request tracing

## Design Principles

1. **Performance First**: Optimize for speed and low latency
2. **Simplicity**: Easy-to-use API
3. **Correctness**: RFC 7540 compliance
4. **Extensibility**: Middleware and hooks
5. **Safety**: Thread-safe where it matters

## References

- [RFC 7540 - HTTP/2](https://tools.ietf.org/html/rfc7540)
- [RFC 7541 - HPACK](https://tools.ietf.org/html/rfc7541)
- [gnet Documentation](https://github.com/panjf2000/gnet)
- [Go net/http2](https://pkg.go.dev/golang.org/x/net/http2)

