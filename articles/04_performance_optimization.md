# Achieving 150k+ RPS - Performance Optimization

**Celeris: Road to v0.1.0**

*October 31, 2025*

---

## Introduction

With HTTP/2 compliance and dual-protocol support working, I finally had time to look at performance. The initial numbers were... not great. This article documents the optimization process—mostly learning the hard way about where Go allocates memory and how gnet works under the hood.

Fair warning: I'm not a performance expert. A lot of this was trial and error with pprof, reading gnet's source code, and Stack Overflow. But the numbers did improve significantly, so maybe this is useful for others in similar situations.

---

## Initial State

### Baseline Performance

First benchmark run:

```
=== Celeris HTTP/2 ===
simple: 48,231 RPS (p95: 12.3ms)
json:   41,892 RPS (p95: 15.7ms)
params: 39,104 RPS (p95: 18.2ms)
```

### Profile Analysis

```bash
$ go tool pprof -top cpu.pprof

Showing nodes accounting for 14540ms, 72.70% of 20000ms total
      flat  flat%   sum%        cum   cum%
    2908ms 14.54% 14.54%     2908ms 14.54%  runtime.mallocgc
    1892ms  9.46% 24.00%     1892ms  9.46%  runtime.memclrNoHeapPointers
    4776ms 23.88% 47.88%     4776ms 23.88%  runtime.scanobject
    2510ms 12.55% 60.43%     2510ms 12.55%  runtime.(*mspan).heapBitsSmallForAddr
```

**Diagnosis**: 47% of CPU time spent in GC-related functions. Excessive allocations causing GC pressure.

---

## Optimization Strategy

For each optimization:
1. **Profile** to identify hotspot
2. **Fix** with minimal change
3. **Validate** compliance (h2spec, tests)
4. **Measure** impact
5. **Re-profile** to find next hotspot

---

## Optimization 1: Buffer Pooling

### Problem

Each stream allocated new buffers:

```go
func NewStream(id uint32) *Stream {
    return &Stream{
        Data:           new(bytes.Buffer),   // Allocation
        OutboundBuffer: new(bytes.Buffer),   // Allocation
    }
}
```

At 50k RPS, that's 100k buffer allocations per second.

### Solution

```go
var bufPool = sync.Pool{
    New: func() any { return new(bytes.Buffer) },
}

func getBuf() *bytes.Buffer {
    return bufPool.Get().(*bytes.Buffer)
}

func putBuf(buf *bytes.Buffer) {
    if buf != nil {
        buf.Reset()
        bufPool.Put(buf)
    }
}

func NewStream(id uint32) *Stream {
    return &Stream{
        Data:           getBuf(),  // Reused
        OutboundBuffer: getBuf(),  // Reused
    }
}

func (s *Stream) Close() {
    putBuf(s.Data)
    putBuf(s.OutboundBuffer)
}
```

**Impact**: Buffer allocations reduced from O(requests) to O(concurrent-peaks).

---

## Optimization 2: Header Slice Pooling

### Problem

Every response created new header slices:

```go
func (c *Connection) WriteResponse(...) {
    headers := make([][2]string, 0, len+1)  // Allocation per request
    headers = append(headers, [2]string{":status", status})
    // ...
}
```

### Solution

```go
var headersSlicePool = sync.Pool{
    New: func() any {
        s := make([][2]string, 0, 8)
        return &s
    },
}

func (c *Connection) WriteResponse(...) {
    pooled := headersSlicePool.Get().(*[][2]string)
    hdrs := (*pooled)[:0]
    
    hdrs = append(hdrs, [2]string{":status", strconv.Itoa(status)})
    hdrs = append(hdrs, headers...)
    
    // ... use headers ...
    
    *pooled = hdrs[:0]
    headersSlicePool.Put(pooled)
}
```

**Note**: Used `strconv.Itoa(status)` instead of `fmt.Sprintf("%d", status)` to avoid fmt allocation.

**Impact**: 90% reduction in header slice allocations.

---

## Optimization 3: HPACK Encoder Reuse

### Problem

Each response created a new HPACK encoder:

```go
func (c *Connection) WriteResponse(...) {
    encoder := frame.NewHeaderEncoder()  // Allocation per response
    headerBlock, _ := encoder.Encode(headers)
}
```

### The Critical Insight

HPACK encoders maintain a dynamic table that improves compression over time. Creating a new encoder:
1. Allocates memory
2. Resets compression state
3. Loses dynamic table benefits

### Solution

Per-connection encoder reuse under existing write lock:

```go
type Connection struct {
    writeMu       sync.Mutex
    headerEncoder *frame.HeaderEncoder  // Persistent
}

func NewConnection(...) *Connection {
    return &Connection{
        headerEncoder: frame.NewHeaderEncoder(),
    }
}

func (c *Connection) WriteResponse(...) {
    c.writeMu.Lock()
    defer c.writeMu.Unlock()
    
    // Reuse encoder (safe under writeMu)
    headerBlock, _ := c.headerEncoder.EncodeBorrow(headers)
    // ...
}
```

**Impact**: Encoder CPU time dropped from 260ms (profiled) to near-zero.

---

## Optimization 4: Pseudo-Header Caching

### Problem

Common headers looked up repeatedly via map:

```go
func (c *Context) Method() string {
    return c.stream.GetHeader(":method")  // Map lookup every time
}
```

### Solution

Cache pseudo-headers as struct fields:

```go
type Context struct {
    method      string  // Cached
    path        string  // Cached
    scheme      string  // Cached
    authority   string  // Cached
}

func newContext(s *stream.Stream, ...) *Context {
    var method, path, scheme, authority string
    
    for _, h := range s.Headers {
        switch h[0] {
        case ":method":
            method = h[1]
        case ":path":
            path = h[1]
        case ":scheme":
            scheme = h[1]
        case ":authority":
            authority = h[1]
        }
    }
    
    return &Context{
        method:    method,
        path:      path,
        scheme:    scheme,
        authority: authority,
    }
}

func (c *Context) Method() string { return c.method }  // Direct field access
func (c *Context) Path() string   { return c.path }
```

**Impact**: Eliminated map lookups for most-accessed headers.

---

## Optimization 5: Router Parameter Pooling

### Problem

Route parameter extraction allocated maps:

```go
func (r *Router) FindRoute(method, path string) (*Route, map[string]string) {
    params := make(map[string]string)  // Allocation per request
    // ...
}
```

### Solution

Pool with lazy allocation:

```go
var paramsPool = sync.Pool{
    New: func() any {
        m := make(map[string]string, 4)
        return &m
    },
}

func (r *Router) FindRoute(method, path string) (*Route, map[string]string) {
    route := r.match(path)
    
    if !route.HasParams {
        return route, nil  // No allocation for 90% of routes!
    }
    
    pooled := paramsPool.Get().(*map[string]string)
    params := *pooled
    
    // Clear existing entries
    for k := range params {
        delete(params, k)
    }
    
    // Extract params
    for i, name := range route.ParamNames {
        params[name] = route.extractParam(path, i)
    }
    
    return route, params
}
```

**Impact**: Zero allocations for non-parameterized routes.

---

## Optimization 6: gnet Build Tags

### The Discovery

gnet provides specialized build tags for maximum performance:

- `poll_opt`: Optimized poller eliminates hash map for FD-to-connection mapping
- `gc_opt`: Reduces GC latency with more efficient connection storage

### Implementation

```makefile
# Makefile
build:
    go build -tags "poll_opt gc_opt" -o bin/celeris ./cmd/example

test-bench:
    go test -tags "poll_opt gc_opt" -bench=. ./...
```

**Impact**: 5-10% throughput improvement with zero code changes.

---

## Optimization 7: Conn.Context() vs sync.Map

### Problem

Original code used `sync.Map` for per-connection storage:

```go
type Server struct {
    connections sync.Map  // gnet.Conn → *Connection
}

func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
    conn, _ := s.connections.Load(c)  // Lock + hash lookup every packet
    // ...
}
```

### Solution

Use gnet's `Conn.Context()` API:

```go
func (s *Server) OnOpen(c gnet.Conn) {
    conn := NewConnection(c, s.handler)
    c.SetContext(conn)  // Store in connection directly
}

func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
    conn := c.Context().(*Connection)  // Direct pointer dereference
    // ...
}
```

**Impact**: 
- Eliminated sync.Map lookup overhead on every packet
- Reduced lock contention
- Better memory locality

---

## Optimization 8: AsyncWritev Serialization

### The Frame Ordering Bug

Under high load, DATA frames arrived before HEADERS—a protocol violation.

### Root Cause

Multiple `AsyncWritev` calls could complete out of order:

```
Thread 1: AsyncWritev(HEADERS_A) → returns immediately
Thread 1: AsyncWritev(DATA_A) → returns immediately
                                  ↓
                     DATA_A completes first = PROTOCOL_ERROR
```

### Solution

Serialize with inflight tracking:

```go
type connWriter struct {
    mu       sync.Mutex
    pending  [][]byte
    inflight bool
    queued   [][]byte
}

func (w *connWriter) Flush() error {
    w.mu.Lock()
    
    if w.inflight {
        // Queue for later
        w.queued = append(w.queued, w.pending...)
        w.pending = nil
        w.mu.Unlock()
        return nil
    }
    
    batch := w.pending
    w.pending = nil
    w.inflight = true
    w.mu.Unlock()
    
    return w.conn.AsyncWritev(batch, func(...) error {
        w.mu.Lock()
        if len(w.queued) > 0 {
            next := w.queued
            w.queued = nil
            w.mu.Unlock()
            return w.conn.AsyncWritev(next, ...)  // Chain
        }
        w.inflight = false
        w.mu.Unlock()
        return nil
    })
}
```

**Impact**: Zero protocol errors under extreme load.

---

## Final Results

### HTTP/2 Performance

| Scenario | MaxClients | MaxRPS | P95 (ms) |
|----------|------------|--------|----------|
| simple | 600 | 156,892 | 4.12 |
| json | 400 | 121,034 | 2.89 |
| params | 380 | 111,456 | 3.21 |

### Comparison with Other Frameworks

| Framework | Simple (RPS) | vs Celeris |
|-----------|--------------|------------|
| **Celeris** | **156,892** | baseline |
| net/http | 74,521 | +110% |
| Gin | 81,234 | +93% |
| Echo | 79,892 | +96% |
| Chi | 76,102 | +106% |

### HTTP/1.1 Performance

| Scenario | MaxClients | MaxRPS | P95 (ms) |
|----------|------------|--------|----------|
| simple | 480 | 75,031 | 7.40 |
| json | 558 | 74,318 | 8.42 |
| params | 480 | 77,662 | 7.17 |

---

## Profile: Before vs After

### Before Optimization

```
runtime.mallocgc:        14.54% CPU
runtime.scanobject:      23.88% CPU
runtime.memclrNoHeapPointers: 9.46% CPU
Total GC-related:        47.88% CPU
```

### After Optimization

```
runtime.mallocgc:        2.31% CPU
runtime.scanobject:      4.12% CPU
runtime.memclrNoHeapPointers: 1.89% CPU
Total GC-related:        8.32% CPU
```

**GC overhead reduced by 83%!**

---

## Key Takeaways

1. **Profile first**: Don't guess, measure. GC pressure was the main bottleneck.

2. **sync.Pool is your friend**: Reuse buffers, slices, maps—anything allocated per-request.

3. **gnet build tags matter**: `poll_opt` and `gc_opt` provide free performance.

4. **Connection-scoped resources**: HPACK encoders, write serializers benefit from reuse.

5. **Async needs ordering**: AsyncWritev requires explicit serialization for protocol correctness.

6. **Validate constantly**: Every optimization must maintain 147/147 h2spec compliance.

---

## What's Next

In the final article for this v0.1.0 series, we cover the feature additions, testing infrastructure, and lessons learned from the whole journey.

---

*The benchmarks look good on my machine, but real-world performance depends on many factors. Take these numbers as directional, not absolute.*
