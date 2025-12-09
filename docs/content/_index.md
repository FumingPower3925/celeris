---
title: "Celeris Documentation"
weight: 1
---

# Celeris HTTP/2 first framework

Welcome to the Celeris HTTP/2 first framework documentation! Celeris HTTP/2 first framework is a high-performance HTTP/2-only server built on top of gnet for blazing-fast networking and Go's x/net/http2 for protocol compliance.

## What is Celeris HTTP/2 first framework?

Celeris HTTP/2 first framework is designed to be:

- **Fast**: Built on gnet, one of the fastest networking libraries in Go
- **Simple**: Easy-to-use API similar to popular web frameworks
- **Modern**: Focused on HTTP/2 protocol with full multiplexing support
- **Efficient**: Zero-copy optimizations and minimal allocations

## Quick Links

- [Getting Started]({{< relref "getting-started/installation" >}})
- [API Reference]({{< relref "api-reference/server" >}})
- [Examples]({{< relref "examples/basic-server" >}})
- [Architecture]({{< relref "architecture/design" >}})

## Features

### High Performance

Celeris HTTP/2 first framework leverages gnet's event-driven architecture to achieve exceptional performance:

- Handles 100,000+ requests per second
- Sub-millisecond response times
- Minimal memory allocations
- Multi-core support with automatic load balancing

### Simple API

Clean and intuitive API that's easy to learn:

```go
router := celeris.NewRouter()
router.GET("/hello/:name", func(ctx *celeris.Context) error {
    name := celeris.Param(ctx, "name")
    return ctx.JSON(200, map[string]string{
        "message": "Hello, " + name + "!",
    })
})
```

### Powerful Router

High-performance trie-based router with:

- Path parameters (`:param`)
- Wildcards (`*path`)
- Route groups
- Middleware support

### HTTP/2 Only

Full HTTP/2 support including:

- Stream multiplexing
- Flow control
- Header compression (HPACK)
- Server settings negotiation

## Architecture

Celeris HTTP/2 first framework is built in three layers:

1. **Transport Layer**: gnet event handlers for raw TCP connections
2. **Protocol Layer**: HTTP/2 frame parsing and stream management
3. **Application Layer**: User-friendly API with routing and middleware

## Community

- [GitHub Repository](https://github.com/albertbausili/celeris)
- [Issue Tracker](https://github.com/albertbausili/celeris/issues)
- [Discussions](https://github.com/albertbausili/celeris/discussions)

## License

Celeris HTTP/2 first framework is released under the Apache License 2.0.

