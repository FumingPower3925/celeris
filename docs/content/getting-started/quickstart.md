---
title: "Quick Start"
weight: 2
---

# Quick Start

This guide will help you create your first Celeris server in minutes.

## Basic Server

Create a file `main.go`:

```go
package main

import (
    "log"
    "github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
    // Create a new router
    router := celeris.NewRouter()
    
    // Add global middleware
    router.Use(
        celeris.Recovery(),  // Recover from panics
        celeris.Logger(),    // Log requests
    )
    
    // Define routes
    router.GET("/", homeHandler)
    router.GET("/hello/:name", helloHandler)
    router.POST("/data", dataHandler)
    
    // Create and start server
    server := celeris.NewWithDefaults()
    log.Println("Server starting on :8080")
    log.Fatal(server.ListenAndServe(router))
}

func homeHandler(ctx *celeris.Context) error {
    return ctx.JSON(200, map[string]string{
        "message": "Welcome to Celeris!",
    })
}

func helloHandler(ctx *celeris.Context) error {
    name := celeris.Param(ctx, "name")
    return ctx.JSON(200, map[string]string{
        "message": "Hello, " + name + "!",
    })
}

func dataHandler(ctx *celeris.Context) error {
    var data map[string]interface{}
    if err := ctx.BindJSON(&data); err != nil {
        return ctx.JSON(400, map[string]string{
            "error": "Invalid JSON",
        })
    }
    
    return ctx.JSON(200, map[string]interface{}{
        "received": data,
        "status":   "success",
    })
}
```

## Run the Server

```bash
go run main.go
```

## Test the Server

Test with curl using HTTP/2 cleartext:

```bash
# GET request
curl --http2-prior-knowledge http://localhost:8080/

# GET with parameter
curl --http2-prior-knowledge http://localhost:8080/hello/world

# POST with JSON
curl --http2-prior-knowledge -X POST \
  http://localhost:8080/data \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","age":30}'
```

## Custom Configuration

You can customize the server configuration:

```go
config := celeris.Config{
    Addr:         ":8080",
    Multicore:    true,
    NumEventLoop: 4,
    ReusePort:    true,
}

server := celeris.New(config)
```

## Route Groups

Organize routes with groups:

```go
api := router.Group("/api")
api.Use(authMiddleware)  // Middleware only for this group

api.GET("/users", listUsers)
api.GET("/users/:id", getUser)
api.POST("/users", createUser)
```

## Next Steps

- Learn about [Core Concepts]({{< relref "concepts" >}})
- Explore [Examples]({{< relref "../examples/basic-server" >}})
- Read the [API Reference]({{< relref "../api-reference/server" >}})

