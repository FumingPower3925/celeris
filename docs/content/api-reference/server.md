---
title: "Server"
weight: 1
---

# Server API

The `Server` manages the HTTP/2 server lifecycle.

## Creating a Server

### New

```go
func New(config Config) *Server
```

Creates a new server with the given configuration.

**Example:**

```go
server := celeris.New(celeris.Config{
    Addr:      ":8080",
    Multicore: true,
})
```

### NewWithDefaults

```go
func NewWithDefaults() *Server
```

Creates a new server with default configuration.

**Example:**

```go
server := celeris.NewWithDefaults()
```

## Methods

### ListenAndServe

```go
func (s *Server) ListenAndServe(handler Handler) error
```

Starts the server and blocks until it's stopped.

**Parameters:**
- `handler`: The handler to process requests (usually a `Router`)

**Returns:**
- `error`: Error if the server fails to start

**Example:**

```go
router := celeris.NewRouter()
// ... configure router ...
err := server.ListenAndServe(router)
```

### Start

```go
func (s *Server) Start() error
```

Starts the server (alternative to `ListenAndServe`).

### Stop

```go
func (s *Server) Stop(ctx context.Context) error
```

Gracefully stops the server.

**Parameters:**
- `ctx`: Context for cancellation

**Returns:**
- `error`: Error if shutdown fails

**Example:**

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
err := server.Stop(ctx)
```

## Configuration

### Config

```go
type Config struct {
    Addr                 string
    Multicore            bool
    NumEventLoop         int
    ReusePort            bool
    ReadTimeout          time.Duration
    WriteTimeout         time.Duration
    IdleTimeout          time.Duration
    MaxHeaderBytes       int
    MaxConcurrentStreams uint32
    MaxConnections       uint32
    MaxFrameSize         uint32
    InitialWindowSize    uint32
    Logger               *log.Logger
    DisableKeepAlive     bool
    EnableH1             bool
    EnableH2             bool
}
```

#### Fields

- **Addr** (`string`): Address to listen on (e.g., ":8080")
- **Multicore** (`bool`): Enable multi-core support (default: true)
- **NumEventLoop** (`int`): Number of event loops (0 = auto-detect)
- **ReusePort** (`bool`): Enable SO_REUSEPORT (default: true)
- **ReadTimeout** (`time.Duration`): Max duration for reading requests
- **WriteTimeout** (`time.Duration`): Max duration for writing responses
- **IdleTimeout** (`time.Duration`): Max idle time
- **MaxHeaderBytes** (`int`): Max header size (default: 16MB)
- **MaxConcurrentStreams** (`uint32`): Max concurrent HTTP/2 streams (default: adaptive, min 20000)
- **MaxConnections** (`uint32`): Max concurrent connections (default: adaptive, min 100000)
- **MaxFrameSize** (`uint32`): Max HTTP/2 frame size (default: 256KB)
- **InitialWindowSize** (`uint32`): Initial flow control window (default: 1MB)
- **Logger** (`*log.Logger`): Logger instance
- **DisableKeepAlive** (`bool`): Disable keep-alive connections
- **EnableH1** (`bool`): Enable HTTP/1.1 support (default: true)
- **EnableH2** (`bool`): Enable HTTP/2 support (default: true)

### DefaultConfig

```go
func DefaultConfig() Config
```

Returns a configuration with high-performance defaults optimized for throughput:

- Addr: ":8080"
- Multicore: true
- ReusePort: true
- MaxConcurrentStreams: Adaptive (e.g., 20,000+)
- MaxFrameSize: 262,144 (256KB)
- InitialWindowSize: 1,048,576 (1MB)
- Read/Write Timeout: 300s
- EnableH1: true
- EnableH2: true

## Examples

### Basic Server

```go
server := celeris.NewWithDefaults()
router := celeris.NewRouter()
router.GET("/", handler)
log.Fatal(server.ListenAndServe(router))
```

### Custom Configuration

```go
config := celeris.Config{
    Addr:         ":9000",
    Multicore:    true,
    NumEventLoop: 4,
    ReusePort:    true,
}

server := celeris.New(config)
```

### Graceful Shutdown

```go
server := celeris.NewWithDefaults()

go func() {
    if err := server.ListenAndServe(router); err != nil {
        log.Fatal(err)
    }
}()

// Wait for interrupt signal
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

// Graceful shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := server.Stop(ctx); err != nil {
    log.Fatal(err)
}
```

