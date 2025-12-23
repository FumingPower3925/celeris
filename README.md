# Celeris HTTP/2 first framework

[![Go Report Card](https://goreportcard.com/badge/github.com/FumingPower3925/celeris)](https://goreportcard.com/report/github.com/FumingPower3925/celeris)
[![GoDoc](https://godoc.org/github.com/FumingPower3925/celeris?status.svg)](https://godoc.org/github.com/FumingPower3925/celeris)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

**Celeris HTTP/2 first framework** is a high-performance HTTP/2 first server built on top of [gnet](https://github.com/panjf2000/gnet) for blazing-fast networking and Go's `x/net/http2` for protocol compliance. It's designed to be simple, fast, and efficient.

## Features

- **Ultra-Fast**: Built on gnet, one of the fastest networking libraries in Go
- **Simple API**: Easy-to-use interface similar to popular web frameworks
- **HTTP/2 First**: Optimized for HTTP/2 with HTTP/1.1 fallback support
- **Multiplexing**: Full support for HTTP/2 stream multiplexing
- **Powerful Routing**: High-performance trie-based router with parameters and wildcards
- **Middleware Support**: Built-in Logger, Recovery, CORS, Compression, Rate Limiting
- **Production Ready**: Comprehensive testing, benchmarking, and monitoring
- **Auto-Documentation**: Swagger-style API docs generation

## Installation

```bash
go get -u github.com/FumingPower3925/celeris
```

## Quick Start

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
            "message": "Hello, Celeris HTTP/2 first framework!",
        })
    })
    
    router.GET("/hello/:name", func(ctx *celeris.Context) error {
        name := celeris.Param(ctx, "name")
        return ctx.JSON(200, map[string]string{
            "message": "Hello, " + name + "!",
        })
    })
    
    // Create and start server
    server := celeris.NewWithDefaults()
    log.Fatal(server.ListenAndServe(router))
}
```

## Performance

Celeris HTTP/2 first framework leverages gnet's event-driven architecture to achieve exceptional performance:

- **High Throughput**: Handles 100,000+ requests per second
- **Low Latency**: Sub-millisecond response times
- **Memory Efficient**: Minimal allocations through zero-copy optimizations
- **Scalable**: Multi-core support with automatic load balancing

## Architecture

Celeris HTTP/2 first framework is built in three layers:

1. **Transport Layer** (`internal/mux`): Protocol multiplexing and gnet event handlers
2. **Protocol Layer** (`internal/h1`, `internal/h2`): HTTP/1.1 and HTTP/2 parsing
3. **Application Layer** (`pkg/celeris`): User-friendly API with routing and middleware

## Documentation

Full documentation is available at [https://FumingPower3925.github.io/celeris](https://FumingPower3925.github.io/celeris)

- [Getting Started](docs/content/getting-started/)
- [API Reference](docs/content/api-reference/)
- [Examples](docs/content/examples/)
- [Architecture](docs/content/architecture/)

## Code Examples

We provide comprehensive examples demonstrating all Celeris HTTP/2 first framework features. Each example is self-contained and focuses on specific functionality:

**[View all examples](examples/README.md)** - Complete examples directory with middleware, routing, streaming, and more.

The examples include:
- **Middleware**: Logger, Recovery, CORS, Rate Limiting, Health Checks, Auto Documentation
- **Core Features**: Basic Routing, Streaming, Server Push
- **Advanced**: Request ID tracking, Compression, Error handling

Each example can be run independently:
```bash
cd examples/logger
go run main.go
```

## API Examples

### JSON API

```go
router.POST("/api/users", func(ctx *celeris.Context) error {
    var user struct {
        Name  string `json:"name"`
        Email string `json:"email"`
    }
    
    if err := ctx.BindJSON(&user); err != nil {
        return ctx.JSON(400, map[string]string{"error": "Invalid JSON"})
    }
    
    // Process user...
    
    return ctx.JSON(201, user)
})
```

### Route Groups

```go
api := router.Group("/api/v1")
api.Use(authMiddleware)

api.GET("/users", listUsers)
api.GET("/users/:id", getUser)
api.POST("/users", createUser)
api.PUT("/users/:id", updateUser)
api.DELETE("/users/:id", deleteUser)
```

### Middleware

```go
func authMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        token := ctx.Header().Get("authorization")
        if token == "" {
            return ctx.JSON(401, map[string]string{"error": "Unauthorized"})
        }
        
        // Validate token...
        
        return next.ServeHTTP2(ctx)
    })
}
```

## Development

### Requirements

- Go 1.25.2 or later
- golangci-lint
- Hugo (for documentation)
- h2load (for load testing)
- h2spec (for compliance testing)

### Building

```bash
make build
```

### Testing

```bash
make test
make bench
make coverage
```

### Linting

```bash
make lint
```

### Documentation

```bash
make docs-serve
```

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

Celeris HTTP/2 first framework is released under the Apache License 2.0. See [LICENSE](LICENSE) for details.

## Acknowledgments

- [gnet](https://github.com/panjf2000/gnet) - High-performance networking framework
- [golang.org/x/net/http2](https://pkg.go.dev/golang.org/x/net/http2) - HTTP/2 implementation

## Roadmap

- [x] Server Push support
- [x] Compression middleware (gzip/brotli)
- [x] Rate limiting
- [x] HTTP/1.1 support
- [ ] TLS/HTTPS support
- [ ] HTTP/2 priority handling
- [ ] Graceful shutdown improvements
- [ ] Enhanced metrics and tracing
- [ ] WebSocket support

---

Made with speed in mind by the Celeris HTTP/2 first framework team

