---
title: "Performance"
weight: 2
---

# Performance

Performance characteristics and optimization guide.

## Benchmarks

### Throughput

Typical performance on a modern server (8 cores):

| Metric | Value |
|--------|-------|
| Requests/sec | 100,000+ |
| Latency (p50) | < 1ms |
| Latency (p99) | < 5ms |
| Memory/conn | ~4KB |
| CPU usage | Scales linearly |

### Comparison

Compared to standard net/http:

- **2-3x** higher throughput
- **40-60%** lower latency
- **50%** less memory per connection

## Performance Tips

### 1. Enable Multi-core

```go
config := celeris.Config{
    Multicore: true,
    NumEventLoop: 0, // Auto-detect cores
}
```

Benefits:
- Parallel request processing
- Better CPU utilization
- Scales with core count

### 2. Connection Pooling

Clients should reuse connections:

```go
client := &http.Client{
    Transport: &http2.Transport{
        AllowHTTP: true,
        // Reuse connections
    },
}
```

### 3. Minimize Allocations

Use Context efficiently:

```go
// Good: Direct write
func handler(ctx *celeris.Context) error {
    return ctx.WriteString("Hello")
}

// Bad: Extra allocation
func handler(ctx *celeris.Context) error {
    s := "Hello"
    return ctx.WriteString(s)
}
```

### 4. Buffer Sizes

Configure appropriate buffer sizes:

```go
config := celeris.Config{
    MaxHeaderBytes:    1 << 20,  // 1MB
    MaxFrameSize:      16384,     // 16KB
    InitialWindowSize: 65535,     // 64KB
}
```

### 5. Avoid Blocking

Don't block the handler:

```go
// Good: Async work
func handler(ctx *celeris.Context) error {
    go processAsync(data)
    return ctx.JSON(202, map[string]string{
        "status": "processing",
    })
}

// Bad: Blocks handler
func handler(ctx *celeris.Context) error {
    time.Sleep(10 * time.Second) // Don't do this!
    return ctx.JSON(200, result)
}
```

### 6. Middleware Order

Put cheap middleware first:

```go
router.Use(
    celeris.RequestID(),    // Fast
    celeris.Recovery(),     // Fast
    celeris.Logger(),       // Medium
    authMiddleware,         // Expensive
)
```

### 7. Route Organization

Organize routes for fast matching:

```go
// Good: Common routes first
router.GET("/", homeHandler)
router.GET("/api/users", usersHandler)
router.GET("/api/users/:id", userHandler)

// Avoid: Too many groups
// Each group adds overhead
```

## Load Testing

### Using h2load

```bash
# Install h2load (part of nghttp2)
brew install nghttp2

# Basic load test
h2load -n 10000 -c 100 -m 10 http://localhost:8080/

# Advanced
h2load -n 100000 -c 1000 -m 100 -t 4 \
  --h1 http://localhost:8080/api/test
```

### Using wrk2

```bash
# Install wrk2
git clone https://github.com/giltene/wrk2
cd wrk2 && make

# Run test
./wrk -t4 -c100 -d30s -R1000 http://localhost:8080/
```

### Benchmark Script

```bash
#!/bin/bash
# benchmark.sh

echo "Starting server..."
./bin/example &
SERVER_PID=$!
sleep 2

echo "Running benchmark..."
h2load -n 50000 -c 100 -m 10 http://localhost:8080/ > results.txt

echo "Results:"
cat results.txt

kill $SERVER_PID
```

## Profiling

### CPU Profiling

```go
import (
    "runtime/pprof"
    "os"
)

func main() {
    // Start CPU profiling
    f, _ := os.Create("cpu.prof")
    pprof.StartCPUProfile(f)
    defer pprof.StopCPUProfile()
    
    // Run server
    server.ListenAndServe(router)
}
```

Analyze:
```bash
go tool pprof cpu.prof
(pprof) top10
(pprof) list functionName
```

### Memory Profiling

```go
import (
    "runtime/pprof"
    "os"
)

func profileMemory() {
    f, _ := os.Create("mem.prof")
    pprof.WriteHeapProfile(f)
    f.Close()
}
```

Analyze:
```bash
go tool pprof mem.prof
(pprof) top10
(pprof) list functionName
```

### HTTP Profiling

Enable pprof endpoint:

```go
import _ "net/http/pprof"

func main() {
    // Profiling server on different port
    go func() {
        http.ListenAndServe(":6060", nil)
    }()
    
    // Main server
    server.ListenAndServe(router)
}
```

Access profiles:
```bash
# CPU profile
go tool pprof http://localhost:6060/debug/pprof/profile

# Memory profile
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutines
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

## Monitoring

### Metrics

Implement metrics middleware:

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    requestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "http_request_duration_seconds",
            Help: "HTTP request duration",
        },
        []string{"method", "path", "status"},
    )
)

func MetricsMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        start := time.Now()
        err := next.ServeHTTP2(ctx)
        duration := time.Since(start).Seconds()
        
        requestDuration.WithLabelValues(
            ctx.Method(),
            ctx.Path(),
            fmt.Sprintf("%d", ctx.statusCode),
        ).Observe(duration)
        
        return err
    })
}
```

### Health Check

```go
router.GET("/health", func(ctx *celeris.Context) error {
    return ctx.JSON(200, map[string]string{
        "status": "healthy",
        "uptime": fmt.Sprintf("%v", time.Since(startTime)),
    })
})

router.GET("/ready", func(ctx *celeris.Context) error {
    // Check dependencies
    if !dbReady() {
        return ctx.JSON(503, map[string]string{
            "status": "not ready",
        })
    }
    
    return ctx.JSON(200, map[string]string{
        "status": "ready",
    })
})
```

## Optimization Checklist

- [ ] Multi-core enabled
- [ ] Appropriate buffer sizes
- [ ] Middleware ordered by cost
- [ ] No blocking operations in handlers
- [ ] Connection pooling on clients
- [ ] Proper error handling
- [ ] Metrics and monitoring
- [ ] Regular profiling
- [ ] Load testing in CI

## Common Bottlenecks

### 1. Database Queries

```go
// Bad: Synchronous queries
func handler(ctx *celeris.Context) error {
    users := db.Query("SELECT * FROM users") // Slow!
    return ctx.JSON(200, users)
}

// Good: Use connection pool, prepared statements
var userStmt = db.Prepare("SELECT * FROM users LIMIT ?")

func handler(ctx *celeris.Context) error {
    users, _ := userStmt.Query(100)
    return ctx.JSON(200, users)
}
```

### 2. JSON Encoding

```go
// Bad: Encode in handler
func handler(ctx *celeris.Context) error {
    data := getLargeData()
    return ctx.JSON(200, data) // Encoding blocks
}

// Good: Pre-encode or stream
var cachedData []byte

func handler(ctx *celeris.Context) error {
    return ctx.Data(200, "application/json", cachedData)
}
```

### 3. Logging

```go
// Bad: Synchronous logging
func handler(ctx *celeris.Context) error {
    log.Printf("Request: %v", ctx.Path()) // Blocks
    // ...
}

// Good: Async logging
var logChan = make(chan string, 1000)

func handler(ctx *celeris.Context) error {
    select {
    case logChan <- ctx.Path():
    default:
    }
    // ...
}
```

## Expected Performance

### Requests per Second

| Scenario | RPS |
|----------|-----|
| Hello World | 150,000+ |
| JSON Response | 100,000+ |
| Database Query | 10,000+ |
| Complex Logic | Varies |

### Latency

| Percentile | Target |
|------------|--------|
| p50 | < 1ms |
| p95 | < 5ms |
| p99 | < 10ms |
| p99.9 | < 50ms |

### Resource Usage

| Resource | Usage |
|----------|-------|
| Memory per connection | ~4KB |
| CPU per 10K RPS | ~1 core |
| Goroutines per connection | 1-2 |

## Scaling

### Vertical Scaling

- Add more CPU cores
- Increase memory
- Faster storage

### Horizontal Scaling

- Multiple server instances
- Load balancer (nginx, HAProxy)
- Shared nothing architecture

### Load Balancer Config

nginx example:

```nginx
upstream backend {
    server 127.0.0.1:8080;
    server 127.0.0.1:8081;
    server 127.0.0.1:8082;
    server 127.0.0.1:8083;
}

server {
    listen 80 http2;
    
    location / {
        proxy_pass http://backend;
        proxy_http_version 2.0;
    }
}
```

## Conclusion

Celeris is designed for high performance out of the box. Follow these guidelines to maximize throughput and minimize latency in your applications.

