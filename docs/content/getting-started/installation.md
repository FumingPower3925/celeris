---
title: "Installation"
weight: 1
---

# Installation

## Requirements

- Go 1.25.2 or later
- Linux, macOS, or Windows

## Install Celeris

To install Celeris, use `go get`:

```bash
go get -u github.com/albertbausili/celeris
```

## Verify Installation

Create a simple test file to verify the installation:

```go
package main

import (
    "log"
    "github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
    router := celeris.NewRouter()
    router.GET("/", func(ctx *celeris.Context) error {
        return ctx.String(200, "Celeris is installed!")
    })
    
    server := celeris.NewWithDefaults()
    log.Fatal(server.ListenAndServe(router))
}
```

Run it:

```bash
go run main.go
```

Test with curl (using HTTP/2 cleartext):

```bash
curl --http2-prior-knowledge http://localhost:8080/
```

You should see: `Celeris is installed!`

## Development Tools (Optional)

For development, you may want to install additional tools:

```bash
# golangci-lint for linting
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Hugo for building documentation
brew install hugo  # macOS
# or download from https://gohugo.io/

# h2load for load testing (part of nghttp2)
brew install nghttp2  # macOS

# h2spec for HTTP/2 compliance testing
# Download from https://github.com/summerwind/h2spec
```

## Next Steps

- [Quick Start Guide]({{< relref "quickstart" >}})
- [Core Concepts]({{< relref "concepts" >}})

