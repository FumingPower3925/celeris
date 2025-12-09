# "Production-Ready" (Sort Of): Features, Testing, and Lessons Learned

**Celeris: Road to v0.1.0**

*November 25, 2025*

---

## Introduction

This is the last article in the v0.1.0 series. With protocol compliance and basic performance sorted, I spent time adding the features that make a framework actually usable—middleware, request helpers, observability.

I want to be upfront: Celeris is still rough around the edges. There's no TLS, no WebSocket support, and the documentation could be better. But it's functional enough that I'm comfortable calling it v0.1.0 and sharing the code.

This article covers what I added, how it's tested, and what I learned along the way.

---

## Feature Development Philosophy

For each feature:
1. **Design API** (study Gin, Echo, Chi for ergonomics)
2. **Implement**
3. **Test** (unit, integration, fuzz)
4. **Validate** (`make lint`, `make h2spec`, `make test-race`)
5. **Document**

This ensured every feature met quality standards before merging.

---

## Built-in Middleware

### Logger Middleware

Configurable structured logging:

```go
type LoggerConfig struct {
    Output       io.Writer
    Format       string             // "json" or "text"
    SkipPaths    []string
    CustomFields map[string]string
}

router.Use(celeris.LoggerWithConfig(celeris.LoggerConfig{
    Format:    "json",
    SkipPaths: []string{"/health", "/metrics"},
    CustomFields: map[string]string{
        "service": "api-gateway",
        "version": "1.0.0",
    },
}))
```

**Output**:
```json
{
  "timestamp": "2025-10-16T10:30:00Z",
  "method": "GET",
  "path": "/api/users",
  "status": 200,
  "duration_ms": 5,
  "service": "api-gateway"
}
```

### Compression Middleware

Automatic gzip/brotli compression:

```go
router.Use(celeris.CompressWithConfig(celeris.CompressConfig{
    Level:   6,
    MinSize: 1024,  // Only compress > 1KB
    ExcludedTypes: []string{"image/", "video/"},
}))
```

**Implementation challenge**: HTTP/2 flow control must account for compressed size, not original size.

### Rate Limiter

Token bucket rate limiting:

```go
router.Use(celeris.RateLimiter(100))  // 100 requests/second
```

### Health Check

Automatic health endpoint:

```go
router.Use(celeris.Health())  // Exposes /health
```

### Request ID

Distributed tracing correlation:

```go
router.Use(celeris.RequestID())

// In handler
requestID := ctx.Header().Get("x-request-id")
```

**Implementation note**: Uses `crypto/rand` + atomic counter for guaranteed uniqueness.

### Auto-Documentation

Swagger-style API docs:

```go
router.Use(celeris.DocsWithRouter(router))
// Serves Swagger UI at /docs
```

---

## Request Parsing Helpers

Reducing boilerplate for common operations:

```go
// Query parameters
page, _ := ctx.QueryInt("page")
limit := ctx.QueryDefault("limit", "10")
enabled := ctx.QueryBool("enabled")

// Cookies
session := ctx.Cookie("session")
ctx.SetCookie(&http.Cookie{
    Name:     "session",
    Value:    "abc123",
    HttpOnly: true,
    Secure:   true,
})

// Form data
username := ctx.FormValue("username")

// Request body
var user User
ctx.BindJSON(&user)
```

---

## Static File Serving

With ETag caching and 304 Not Modified support:

```go
router.Static("/static", "./public")

// Serves:
// /static/css/style.css → ./public/css/style.css
// /static/js/app.js → ./public/js/app.js
```

**Features**:
- Content-Type detection by extension
- ETag generation (modtime + size)
- If-None-Match → 304 response
- Cache-Control headers
- Path traversal prevention

---

## Streaming and SSE

Server-Sent Events for real-time updates:

```go
router.GET("/events", func(ctx *celeris.Context) error {
    ctx.SetHeader("content-type", "text/event-stream")
    ctx.SetHeader("cache-control", "no-cache")
    
    for i := 0; i < 10; i++ {
        ctx.SSE(celeris.SSEEvent{
            Event: "update",
            Data:  fmt.Sprintf("Message %d", i),
        })
        ctx.Flush()
        time.Sleep(time.Second)
    }
    
    return nil
})
```

**HTTP/2 advantage**: SSE streams multiplex with other requests on the same connection.

---

## Error Handling

Structured HTTP errors:

```go
type HTTPError struct {
    Code    int
    Message string
    Details map[string]interface{}
}

func (e *HTTPError) Error() string {
    return fmt.Sprintf("%d: %s", e.Code, e.Message)
}

// Usage
return celeris.NewHTTPError(404, "User not found")

// With details
return &celeris.HTTPError{
    Code:    400,
    Message: "Validation failed",
    Details: map[string]interface{}{
        "field": "email",
        "error": "invalid format",
    },
}
```

---

## Test Infrastructure

### Test Categories

```
test/
├── fuzzy/        # Fuzz testing for edge cases
├── integration/  # End-to-end HTTP tests
├── benchmark/    # Performance benchmarks
└── load/         # Stress testing

pkg/celeris/      # Unit tests
├── *_test.go
```

### Unit Tests

```go
func TestRouter_Match(t *testing.T) {
    router := NewRouter()
    router.GET("/users/:id", handler)
    
    route, params := router.FindRoute("GET", "/users/123")
    
    if route == nil {
        t.Fatal("expected route match")
    }
    if params["id"] != "123" {
        t.Errorf("expected id=123, got %s", params["id"])
    }
}
```

### Integration Tests

Real HTTP/2 requests against running server:

```go
func TestConcurrentRequests(t *testing.T) {
    server := startTestServer(t)
    defer server.Stop(context.Background())
    
    client := &http.Client{
        Transport: &http2.Transport{AllowHTTP: true},
    }
    
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            resp, err := client.Get("http://localhost:8080/test")
            if err != nil {
                t.Errorf("request failed: %v", err)
            }
            resp.Body.Close()
        }()
    }
    wg.Wait()
}
```

### Fuzz Tests

Find edge cases in parsing:

```go
func FuzzRouterPaths(f *testing.F) {
    f.Add("/users/123")
    f.Add("/api/v1/posts")
    f.Add("/../../../etc/passwd")
    
    f.Fuzz(func(t *testing.T, path string) {
        router := NewRouter()
        router.GET("/*path", handler)
        
        // Should not panic
        route, _ := router.FindRoute("GET", path)
        _ = route
    })
}
```

### Race Detection

```bash
$ make test-race
go test -race ./...
```

Caught critical races in:
- Timeout middleware (double-write to context)
- HPACK decoder recreation
- Stream state transitions

---

## Critical Bugs Fixed

### Race Condition in Timeout Middleware

**Bug**: Handler and timeout goroutine both writing to context.

```go
// BEFORE - Race!
go func() {
    done <- next.ServeHTTP2(ctx)  // Writes to ctx
}()

select {
case err := <-done:
    return err
case <-timeoutCtx.Done():
    return ctx.String(504, "Timeout")  // Also writes to ctx!
}
```

**Fix**: Atomic flag + mutex protection:

```go
responded := atomic.Bool{}

go func() {
    err := next.ServeHTTP2(ctx)
    responded.Store(true)
    done <- err
}()

select {
case err := <-done:
    return err
case <-timeoutCtx.Done():
    if !responded.Load() {
        return ctx.String(504, "Timeout")
    }
    return <-done
}
```

### HPACK Decoder Recreation

**Bug**: Decoder recreated per-request, resetting compression state.

```go
// BEFORE - Bug!
func handleHeaders(...) {
    decoder := hpack.NewDecoder(4096, nil)  // New decoder!
}
```

**Fix**: Connection-scoped decoder:

```go
type Processor struct {
    hpackDecoder *hpack.Decoder  // Persistent
}

func NewProcessor(...) *Processor {
    return &Processor{
        hpackDecoder: hpack.NewDecoder(4096, nil),
    }
}
```

### Header Case Inconsistency

**Bug**: Set stored mixed case, Get looked up lowercase.

```go
headers.Set("Content-Type", "text/plain")  // Stored as "Content-Type"
headers.Get("content-type")                 // Looked for "content-type"
// Not found! → HPACK compression error
```

**Fix**: Always normalize to lowercase:

```go
func (h *Headers) Set(key, value string) {
    lower := strings.ToLower(key)
    h.index[lower] = len(h.headers)
    h.headers = append(h.headers, [2]string{lower, value})
}
```

---

## Lessons Learned

### 1. Protocol Compliance is Non-Negotiable

h2spec tests edge cases that real clients never trigger. But passing them means the implementation is robust:
- Window = 0 handling
- Negative window deltas
- Mid-header block interruptions

### 2. Async I/O Requires Explicit Ordering

gnet's `AsyncWrite` returns before data is sent. For protocols requiring ordering (HTTP/2), explicit serialization is mandatory.

### 3. Connection-Scoped State Matters

HPACK, flow control, and settings are all connection-scoped. Per-request re-initialization breaks protocols.

### 4. Profile Before Optimizing

Initial assumption: "slow parsing." Reality: 47% CPU in GC. Profiling revealed the actual bottleneck.

### 5. Test at Every Layer

- **Unit tests**: Fast feedback on individual components
- **Integration tests**: End-to-end protocol correctness
- **Fuzz tests**: Edge cases humans miss
- **Race detector**: Concurrency bugs
- **h2spec**: Protocol compliance

### 6. Developer Experience Matters

Fast isn't enough. Middleware, helpers, and documentation make the difference between "impressive benchmark" and "production-ready framework."

---

## v0.1.0 Feature Summary

| Category | Features |
|----------|----------|
| **Protocols** | HTTP/2 (100% h2spec), HTTP/1.1, auto-detection |
| **Routing** | Trie-based, parameters, wildcards, groups |
| **Middleware** | Logger, Recovery, CORS, Compress, RateLimiter, RequestID, Health, Docs |
| **Request** | Query, Cookies, Forms, JSON binding |
| **Response** | JSON, HTML, String, File, Streaming, SSE |
| **Performance** | 156k RPS (simple), 121k RPS (JSON) |
| **Testing** | Unit, Integration, Fuzz, Race, h2spec |

---

## Future Roadmap

Building on v0.1.0:
- [ ] TLS/HTTPS support
- [ ] HTTP/2 priority scheduling
- [ ] WebSocket support
- [ ] Enhanced metrics and tracing
- [ ] Graceful shutdown improvements

---

## Conclusion

Celeris v0.1.0 is done—at least, this iteration of it. It's not perfect:
- No TLS support yet
- No WebSocket support
- Documentation needs work
- Probably bugs I haven't found

But it works. HTTP/2 compliance is 100%, performance is reasonable, and the API is usable. For a learning project that grew into something more, I'm okay with that.

The journey from "I wonder if I can build this" to v0.1.0 taught me a lot about HTTP/2, event-driven programming, and Go performance. Hopefully these articles are useful for others exploring similar territory.

---

*This is v0.1.0—there's more to do. Celeris is open source: [github.com/FumingPower3925/celeris](https://github.com/FumingPower3925/celeris)*
