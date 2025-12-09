---
title: "Core Concepts"
weight: 3
---

# Core Concepts

Understanding these core concepts will help you use Celeris HTTP/2 first framework effectively.

## HTTP/2 Cleartext (h2c)

Celeris HTTP/2 first framework uses HTTP/2 without TLS (cleartext), also known as h2c. This provides:

- **Performance**: All HTTP/2 benefits without TLS overhead
- **Simplicity**: No certificate management
- **Use Case**: Internal services, behind reverse proxies

**Note**: For production internet-facing services, use a reverse proxy (nginx, Envoy) with TLS in front of Celeris HTTP/2 first framework.

## Server

The `Server` is the main component that manages the HTTP/2 server:

```go
server := celeris.New(config)
server.ListenAndServe(router)
```

It handles:
- Connection management
- HTTP/2 protocol negotiation
- Request routing

## Router

The `Router` manages URL routing and middleware:

```go
router := celeris.NewRouter()
router.GET("/path", handler)
router.POST("/path", handler)
```

Features:
- Path parameters: `/users/:id`
- Wildcards: `/files/*path`
- Route groups
- Middleware chains

## Context

The `Context` provides access to request/response data:

```go
func handler(ctx *celeris.Context) error {
    // Read request
    method := ctx.Method()
    path := ctx.Path()
    body, _ := ctx.BodyBytes()
    
    // Write response
    return ctx.JSON(200, data)
}
```

Key methods:
- Request: `Method()`, `Path()`, `Header()`, `Body()`
- Response: `JSON()`, `String()`, `HTML()`, `Data()`
- Parameters: Use `celeris.Param(ctx, "name")`

## Handler

A `Handler` processes requests:

```go
type Handler interface {
    ServeHTTP2(ctx *Context) error
}

// Function adapter
type HandlerFunc func(ctx *Context) error
```

Handlers can:
- Read request data
- Process business logic
- Write responses
- Return errors

## Middleware

Middleware wraps handlers to add functionality:

```go
func LoggerMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        start := time.Now()
        err := next.ServeHTTP2(ctx)
        duration := time.Since(start)
        log.Printf("%s %s - %v", ctx.Method(), ctx.Path(), duration)
        return err
    })
}

router.Use(LoggerMiddleware)
```

Built-in middleware:
- `Recovery()` - Panic recovery
- `Logger()` - Request logging
- `CORS()` - CORS headers
- `RequestID()` - Request ID tracking

## HTTP/2 Streams

HTTP/2 uses streams for multiplexing:

- Multiple requests/responses over one connection
- Streams are independent
- Bidirectional communication
- Flow control per stream

Celeris HTTP/2 first framework handles this automatically. You just write handlers as if they're regular HTTP requests.

## Flow Control

HTTP/2 flow control prevents overwhelming receivers:

- Window-based flow control
- Per-stream and connection-level
- Automatic WINDOW_UPDATE frames

Celeris HTTP/2 first framework manages flow control internally, but you can observe it in logs.

## Performance Tips

1. **Reuse Objects**: The `Context` reuses internal buffers
2. **Minimize Allocations**: Use `ctx.Write()` instead of creating large strings
3. **Stream Responses**: For large responses, write data incrementally
4. **Connection Pooling**: Clients should reuse HTTP/2 connections

## Architecture

Celeris HTTP/2 first framework has three layers:

1. **Transport** (`internal/transport`)
   - gnet event loop
   - Raw TCP handling
   - Connection lifecycle

2. **Protocol** (`internal/frame`, `internal/stream`)
   - HTTP/2 frame parsing
   - Stream management
   - Flow control

3. **Application** (`pkg/celeris`)
   - Router
   - Handlers
   - Middleware
   - Context

## Next Steps

- [Basic Server Example]({{< relref "../examples/basic-server" >}})
- [API Reference]({{< relref "../api-reference/server" >}})
- [Architecture Details]({{< relref "../architecture/design" >}})

