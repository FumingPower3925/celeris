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
    MaxFrameSize         uint32
    InitialWindowSize    uint32
    Logger               *log.Logger
    DisableKeepAlive     bool
}
```

#### Fields

- **Addr** (`string`): Address to listen on (e.g., ":8080")
- **Multicore** (`bool`): Enable multi-core support (default: true)
- **NumEventLoop** (`int`): Number of event loops (0 = auto-detect)
- **ReusePort** (`bool`): Enable SO_REUSEPORT (default: true)
- **ReadTimeout** (`time.Duration`): Maximum duration for reading requests
- **WriteTimeout** (`time.Duration`): Maximum duration for writing responses
- **IdleTimeout** (`time.Duration`): Maximum idle time
- **MaxHeaderBytes** (`int`): Maximum header size
- **MaxConcurrentStreams** (`uint32`): Max concurrent HTTP/2 streams
- **MaxFrameSize** (`uint32`): Maximum HTTP/2 frame size
- **InitialWindowSize** (`uint32`): Initial flow control window
- **Logger** (`*log.Logger`): Logger instance
- **DisableKeepAlive** (`bool`): Disable keep-alive connections

### DefaultConfig

```go
func DefaultConfig() Config
```

Returns a configuration with sensible defaults:

- Addr: ":8080"
- Multicore: true
- ReusePort: true
- MaxConcurrentStreams: 100
- MaxFrameSize: 16384
- InitialWindowSize: 65535

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

