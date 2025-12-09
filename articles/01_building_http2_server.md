# Building a High-Performance HTTP/2 Server from Scratch with Go

**Celeris: Road to v0.1.0**

*July 15, 2025*

---

## Introduction

This is the first article in a series documenting the creation of **Celeris**, an HTTP/2-first server framework for Go. Celeris (Latin for "swift") combines the event-driven networking of [gnet](https://github.com/panjf2000/gnet) with Go's standard HTTP/2 implementation.

This is a learning project that grew into something more substantial. It's far from complete—there's no TLS support yet, no WebSockets, and plenty of rough edges. But the journey from "I wonder if I can build this" to a working HTTP/2 server taught me a lot, and I wanted to share that process.

### Why Build Another HTTP Server?

Honestly, partly curiosity and partly frustration. I wanted to understand:
- How HTTP/2 actually works under the hood
- Why event-driven servers are faster
- What makes frameworks like Gin so ergonomic

Existing Go solutions have trade-offs:
- `net/http`: Goroutine-per-connection model can struggle under extreme load
- Gin/Echo/Chi: Built on `net/http`, inheriting its characteristics
- Custom HTTP/2: Complex to implement correctly (RFC 7540 is 96 pages)

### The Goal

Build a framework that:
1. Leverages gnet's event-driven, zero-copy networking
2. Maintains full HTTP/2 compliance using `golang.org/x/net/http2`
3. Provides a familiar, Express.js/Gin-like API
4. Achieves production quality with comprehensive testing

---

## Technical Decisions

### Core Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Language | Go 1.25.2 | Performance, concurrency, ecosystem |
| Networking | gnet v2.9.4 | Event-driven, multi-reactor, zero-copy |
| HTTP/2 | golang.org/x/net/http2 | RFC-compliant frame handling |
| Protocol | h2c (cleartext HTTP/2) | Simplifies development, TLS optional |

### The Three-Layer Architecture

```
┌─────────────────────────────────────────────────────────┐
│                 Application Layer                        │
│              (pkg/celeris/)                              │
│   Router, Context, Handler, Middleware, Server           │
├─────────────────────────────────────────────────────────┤
│                  Protocol Layer                          │
│         (internal/h1/, internal/h2/)                     │
│   Frame parsing, stream management, HPACK, flow control  │
├─────────────────────────────────────────────────────────┤
│                 Transport Layer                          │
│              (internal/mux/)                             │
│   gnet integration, connection handling, I/O             │
└─────────────────────────────────────────────────────────┘
```

**Layer 1 - Transport**: Implements gnet's `EventHandler` interface:
- `OnBoot()`: Initialize event loops
- `OnOpen()`: Handle new TCP connections
- `OnTraffic()`: Process incoming data
- `OnClose()`: Clean up resources

**Layer 2 - Protocol**: HTTP/2 implementation:
- Frame parser wrapping `http2.Framer`
- Stream state machine (Idle → Open → Half-Closed → Closed)
- HPACK header compression/decompression
- Flow control with connection and stream-level windows
- Priority tree for stream dependencies

**Layer 3 - Application**: Developer-facing API:
- Trie-based router with parameters and wildcards
- Request context with type-safe accessors
- Middleware chaining
- Response helpers (JSON, HTML, String, etc.)

---

## The Critical Challenge: Bridging gnet and x/net/http2

The biggest technical hurdle was connecting two incompatible paradigms:

**gnet**: Event-driven, callback-based
```go
type EventHandler interface {
    OnTraffic(c gnet.Conn) gnet.Action
}
```

**x/net/http2**: Frame-based, synchronous reader
```go
framer := http2.NewFramer(writer, reader)
frame, err := framer.ReadFrame()  // Blocks until frame available
```

### The Incompatibility

- gnet provides bytes asynchronously via callbacks
- `http2.Framer` expects a blocking `io.Reader`
- gnet's `AsyncWrite` returns before data is sent
- HTTP/2 requires strict frame ordering

### The Solution: Custom Buffering Layer

```go
type Connection struct {
    buffer *bytes.Buffer  // Accumulates bytes from gnet
    parser *frame.Parser  // Wraps http2.Framer
}

func (c *Connection) HandleData(ctx context.Context, data []byte) error {
    // Buffer incoming data
    c.buffer.Write(data)
    
    // Parse frames when enough data is available
    for c.buffer.Len() >= 9 {  // HTTP/2 frame header is 9 bytes
        frame, err := c.parser.Parse(c.buffer)
        if err == io.ErrUnexpectedEOF {
            break  // Need more data
        }
        
        // Process complete frame
        c.processor.ProcessFrame(ctx, frame)
    }
    
    return nil
}
```

**Key Insight**: Use a buffer to bridge async byte arrival (gnet) with sync frame parsing (http2.Framer).

---

## Data Flow: Request to Response

```
1. TCP data arrives
   └─→ gnet calls OnTraffic()

2. Connection.HandleData()
   ├─→ Parse HTTP/2 connection preface
   ├─→ Parse SETTINGS frame (negotiate parameters)
   └─→ Parse HEADERS frame
       └─→ HPACK decode headers

3. Processor.ProcessFrame()
   ├─→ Validate stream state
   ├─→ Create Stream object
   └─→ Start handler goroutine
       └─→ HandleStream(ctx, stream)

4. Router matches path
   ├─→ Extract route parameters
   ├─→ Create Context
   ├─→ Run middleware chain
   └─→ Execute handler
       └─→ ctx.JSON(200, data)

5. Context.JSON()
   └─→ Connection.WriteResponse()
       ├─→ HPACK encode response headers
       ├─→ Write HEADERS frame
       ├─→ Write DATA frame(s)
       └─→ AsyncWritev to gnet

6. gnet sends TCP data
   └─→ Async completion callback
```

---

## The Developer API

Celeris provides a familiar, ergonomic API:

```go
package main

import (
    "log"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
    // Create router
    router := celeris.NewRouter()
    
    // Add middleware
    router.Use(celeris.Recovery(), celeris.Logger())
    
    // Define routes
    router.GET("/", func(ctx *celeris.Context) error {
        return ctx.JSON(200, map[string]string{
            "message": "Hello, Celeris!",
        })
    })
    
    router.GET("/users/:id", func(ctx *celeris.Context) error {
        id := celeris.Param(ctx, "id")
        return ctx.JSON(200, map[string]string{
            "user_id": id,
        })
    })
    
    // Route groups
    api := router.Group("/api/v1")
    api.Use(authMiddleware)
    api.GET("/posts", listPosts)
    api.POST("/posts", createPost)
    
    // Start server
    server := celeris.NewWithDefaults()
    log.Fatal(server.ListenAndServe(router))
}
```

### Key API Features

| Feature | Example |
|---------|---------|
| Route parameters | `/users/:id` → `ctx.Param("id")` |
| Wildcards | `/files/*path` → catches all paths |
| Route groups | `router.Group("/api")` |
| Middleware | `router.Use(celeris.Logger())` |
| JSON response | `ctx.JSON(200, data)` |
| Query params | `ctx.Query("page")` |
| Request body | `ctx.BindJSON(&user)` |

---

## Stream State Machine

HTTP/2 streams follow a strict state machine defined in RFC 7540:

```
                          +--------+
                  send PP |        | recv PP
                 ,--------|  idle  |--------.
                /         |        |         \
               v          +--------+          v
        +----------+          |           +----------+
        |          |          | send H /  |          |
,------| reserved |          | recv H    | reserved |------.
|      | (local)  |          |           | (remote) |      |
|      +----------+          v           +----------+      |
|          |             +--------+             |          |
|          |     recv ES |        | send ES    |          |
|   send H |     ,-------|  open  |-------.    | recv H   |
|          |    /        |        |        \   |          |
|          v   v         +--------+         v  v          |
|      +----------+          |           +----------+      |
|      |   half   |          |           |   half   |      |
|      |  closed  |          | send R /  |  closed  |      |
|      | (remote) |          | recv R    | (local)  |      |
|      +----------+          |           +----------+      |
|           |                |                 |           |
|           | send ES /      |       recv ES / |           |
|           | send R /       v        send R / |           |
|           | recv R     +--------+   recv R   |           |
| send R /  `----------->|        |<-----------'  send R / |
| recv R                 | closed |               recv R   |
`----------------------->|        |<-----------------------'
                         +--------+

Legend:
  H:  HEADERS frame
  PP: PUSH_PROMISE frame
  ES: END_STREAM flag
  R:  RST_STREAM frame
```

**Implementation**:
```go
type State int

const (
    StateIdle State = iota
    StateOpen
    StateHalfClosedLocal
    StateHalfClosedRemote
    StateClosed
)

func (s *Stream) SetState(newState State) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Validate transition
    if !isValidTransition(s.state, newState) {
        return fmt.Errorf("invalid state transition: %v → %v", s.state, newState)
    }
    
    s.state = newState
    return nil
}
```

---

## HPACK: Header Compression

HTTP/2 uses HPACK (RFC 7541) for header compression. A critical discovery during development:

**HPACK decoders must persist per connection, not per request!**

```go
type Processor struct {
    hpackDecoder *hpack.Decoder  // Persistent per connection
}

func NewProcessor(...) *Processor {
    return &Processor{
        hpackDecoder: hpack.NewDecoder(4096, nil),  // Once per connection
    }
}
```

**Why?** HPACK maintains a dynamic table that learns common header patterns:
- First request: `content-type: application/json` encoded fully
- Subsequent requests: Same header encoded as single index byte
- Recreating decoder resets this state, breaking compression

---

## First Milestone: Working Prototype

After implementing the core architecture, the first working server:

```go
// Minimal viable HTTP/2 server
server := &Server{handler: simpleHandler}
gnet.Run(server, "tcp://0.0.0.0:8080")
```

At this point:
- ✅ HTTP/2 connection preface handling
- ✅ SETTINGS frame exchange
- ✅ HEADERS frame parsing with HPACK
- ✅ Basic DATA frame handling
- ✅ Response writing via AsyncWritev

**Not yet working**:
- ❌ Flow control
- ❌ Stream priorities
- ❌ CONTINUATION frames
- ❌ Server push
- ❌ Error handling edge cases

The journey to 100% h2spec compliance would address all of these—covered in Part 2.

---

## Key Takeaways

1. **Event-driven + frame-based = buffering**: Bridge async callbacks with sync parsers using buffers
2. **State machines matter**: HTTP/2 stream states must be enforced precisely
3. **Connection-scoped state**: HPACK, flow control windows, and settings are per-connection
4. **Familiar APIs**: Developer experience doesn't have to suffer for performance
5. **Separation of concerns**: Three-layer architecture keeps complexity manageable

---

## What's Next

In Part 2, we'll cover the journey to 100% HTTP/2 compliance—fixing 102 failing h2spec tests across flow control, CONTINUATION frames, error handling, and dozens of protocol edge cases.

---

*This is a work in progress. Celeris is open source: [github.com/FumingPower3925/celeris](https://github.com/FumingPower3925/celeris)*
