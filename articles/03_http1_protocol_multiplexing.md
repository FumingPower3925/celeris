# Adding HTTP/1.1 Support - Protocol Multiplexing

**Celeris: Road to v0.1.0**

*September 28, 2025*

---

## Introduction

With 100% HTTP/2 compliance achieved, Celeris was protocol-correct but HTTP/2-only. That's a problem in practice—many real-world scenarios still need HTTP/1.1:
- Legacy clients and crawlers
- Simple debugging with curl (without `--http2-prior-knowledge`)
- Load balancers performing health checks
- Gradual migration from existing infrastructure

This article covers adding HTTP/1.1 support alongside HTTP/2 with automatic protocol detection on a single port. It's not as elegant as I'd like—there's definitely room for cleanup—but it works.

---

## The Architecture Shift

### Before: HTTP/2 Only

```
internal/
├── frame/       # HTTP/2 frames
├── stream/      # HTTP/2 streams
└── transport/   # HTTP/2 transport
```

### After: Multi-Protocol

```
internal/
├── h1/                    # HTTP/1.1 implementation
│   ├── connection.go      # Connection handling
│   ├── parser.go          # Zero-copy HTTP/1.1 parser
│   ├── response_writer.go # Response writer
│   └── server.go          # gnet integration
├── h2/                    # HTTP/2 implementation
│   ├── frame/             # Frame handling
│   ├── stream/            # Stream management
│   └── transport/         # Connection handling
└── mux/                   # Protocol multiplexer
    └── server.go          # Auto-detection & routing
```

### Design Principles

1. **Clarity**: Protocol-specific code isolated in dedicated packages
2. **Modularity**: Each protocol evolves independently
3. **Unified Interface**: Single handler for both protocols
4. **Zero Performance Penalty**: Detection adds minimal overhead

---

## Protocol Detection

### The Algorithm

HTTP/2 connections start with a specific 24-byte preface:
```
PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n
```

HTTP/1.1 requests start with methods:
```
GET /path HTTP/1.1\r\n
POST /path HTTP/1.1\r\n
```

Detection requires only 4 bytes:

```go
const (
    http2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
    minDetectBytes = 4  // "GET ", "POST", "PRI "
)

func detectProtocol(buf []byte) Protocol {
    if len(buf) < minDetectBytes {
        return ProtocolUnknown
    }
    
    // HTTP/2 detection
    if bytes.HasPrefix(buf, []byte("PRI ")) {
        return ProtocolH2
    }
    
    // HTTP/1.1 detection (common methods)
    switch {
    case bytes.HasPrefix(buf, []byte("GET ")),
         bytes.HasPrefix(buf, []byte("POST")),
         bytes.HasPrefix(buf, []byte("PUT ")),
         bytes.HasPrefix(buf, []byte("HEAD")),
         bytes.HasPrefix(buf, []byte("DELE")),  // DELETE
         bytes.HasPrefix(buf, []byte("PATC")),  // PATCH
         bytes.HasPrefix(buf, []byte("OPTI")):  // OPTIONS
        return ProtocolH1
    }
    
    return ProtocolUnknown
}
```

### Connection Routing

```go
type connSession struct {
    detected bool
    protocol Protocol
    h1Conn   *h1.Connection
    h2Conn   *h2.Connection
}

func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
    session := c.Context().(*connSession)
    buf, _ := c.Next(-1)
    
    if !session.detected {
        session.protocol = detectProtocol(buf)
        session.detected = true
        
        switch session.protocol {
        case ProtocolH2:
            session.h2Conn = h2.NewConnection(c, s.handler, s.logger)
            session.h2Conn.HandleData(s.ctx, buf)
            
        case ProtocolH1:
            session.h1Conn = h1.NewConnection(c, s.handler, s.logger)
            session.h1Conn.HandleData(buf)
            
        default:
            return gnet.Close
        }
        
        return gnet.None
    }
    
    // Route subsequent data to detected protocol
    if session.h2Conn != nil {
        session.h2Conn.HandleData(s.ctx, buf)
    } else {
        session.h1Conn.HandleData(buf)
    }
    
    return gnet.None
}
```

---

## HTTP/1.1 Implementation

### Zero-Copy Parser

The parser operates directly on buffers without allocating intermediate strings:

```go
type Parser struct {
    buf []byte
    pos int
}

type Request struct {
    Method  string
    Path    string
    Version string
    Host    string
    Headers [][2]string
}

func (p *Parser) ParseRequest(req *Request) (consumed int, err error) {
    // Find end of request line
    lineEnd := bytes.Index(p.buf[p.pos:], []byte("\r\n"))
    if lineEnd < 0 {
        return 0, io.ErrUnexpectedEOF  // Need more data
    }
    
    // Parse request line without allocation
    line := p.buf[p.pos : p.pos+lineEnd]
    parts := bytes.SplitN(line, []byte(" "), 3)
    if len(parts) != 3 {
        return 0, fmt.Errorf("malformed request line")
    }
    
    req.Method = string(parts[0])
    req.Path = string(parts[1])
    req.Version = string(parts[2])
    
    p.pos += lineEnd + 2  // Skip \r\n
    
    // Parse headers
    for {
        lineEnd = bytes.Index(p.buf[p.pos:], []byte("\r\n"))
        if lineEnd < 0 {
            return 0, io.ErrUnexpectedEOF
        }
        
        if lineEnd == 0 {
            // Empty line = end of headers
            p.pos += 2
            break
        }
        
        line = p.buf[p.pos : p.pos+lineEnd]
        colonIdx := bytes.IndexByte(line, ':')
        if colonIdx > 0 {
            name := string(bytes.ToLower(line[:colonIdx]))
            value := string(bytes.TrimSpace(line[colonIdx+1:]))
            req.Headers = append(req.Headers, [2]string{name, value})
            
            if name == "host" {
                req.Host = value
            }
        }
        
        p.pos += lineEnd + 2
    }
    
    return p.pos, nil
}
```

### Chunked Transfer Encoding

HTTP/1.1's chunked encoding allows streaming responses without knowing the total size:

```go
func (p *Parser) ParseChunkedBody() (body []byte, consumed int, err error) {
    var result []byte
    
    for {
        // Read chunk size (hex)
        lineEnd := bytes.Index(p.buf[p.pos:], []byte("\r\n"))
        if lineEnd < 0 {
            return nil, 0, io.ErrUnexpectedEOF
        }
        
        sizeLine := p.buf[p.pos : p.pos+lineEnd]
        size, err := strconv.ParseInt(string(sizeLine), 16, 64)
        if err != nil {
            return nil, 0, err
        }
        
        p.pos += lineEnd + 2
        
        if size == 0 {
            // Final chunk
            p.pos += 2  // Skip trailing \r\n
            break
        }
        
        // Read chunk data
        if p.pos+int(size)+2 > len(p.buf) {
            return nil, 0, io.ErrUnexpectedEOF
        }
        
        result = append(result, p.buf[p.pos:p.pos+int(size)]...)
        p.pos += int(size) + 2  // Skip data + \r\n
    }
    
    return result, p.pos, nil
}
```

### Connection Keep-Alive

HTTP/1.1 connections are keep-alive by default:

```go
type Connection struct {
    conn      gnet.Conn
    parser    *Parser
    writer    *ResponseWriter
    handler   Handler
    buffer    *bytes.Buffer
    keepAlive bool
}

func (c *Connection) HandleData(data []byte) error {
    c.buffer.Write(data)
    
    for {
        req := &Request{}
        consumed, err := c.parser.ParseRequest(req)
        if err == io.ErrUnexpectedEOF {
            break  // Need more data
        }
        if err != nil {
            return err
        }
        
        // Read body if Content-Length present
        body := c.readBody(req)
        
        // Convert to unified stream interface
        stream := c.requestToStream(req, body)
        
        // Handle request
        c.handler.HandleStream(stream.ctx, stream)
        
        // Check keep-alive
        c.keepAlive = c.shouldKeepAlive(req)
        
        // Compact buffer
        c.buffer = bytes.NewBuffer(c.buffer.Bytes()[consumed:])
    }
    
    if !c.keepAlive {
        c.conn.Close()
    }
    
    return nil
}
```

---

## Unified Handler Interface

The key insight: convert HTTP/1.1 requests to the same `stream.Stream` interface used by HTTP/2:

```go
func (c *Connection) requestToStream(req *Request, body []byte) *stream.Stream {
    s := stream.NewStream(1)  // Stream ID 1 for HTTP/1.1
    
    // Add HTTP/2-style pseudo-headers
    s.AddHeader(":method", req.Method)
    s.AddHeader(":path", req.Path)
    s.AddHeader(":scheme", "http")
    s.AddHeader(":authority", req.Host)
    
    // Add regular headers
    for _, h := range req.Headers {
        s.AddHeader(h[0], h[1])
    }
    
    // Attach body
    if len(body) > 0 {
        s.AddData(body)
    }
    
    // Mark request as complete
    s.EndStream = true
    s.SetState(stream.StateHalfClosedRemote)
    
    return s
}
```

**Result**: A single handler function works for both HTTP/1.1 and HTTP/2:

```go
router.GET("/api/users", func(ctx *celeris.Context) error {
    // Same code handles both protocols!
    return ctx.JSON(200, users)
})
```

---

## Configuration Options

Protocols can be enabled/disabled independently:

```go
type Config struct {
    Addr           string
    EnableH1       bool  // Default: true
    EnableH2       bool  // Default: true
    MaxConnections int
    // ...
}

// HTTP/2 only (maximum performance)
server := celeris.New(celeris.Config{
    Addr:     ":8080",
    EnableH1: false,
    EnableH2: true,
})

// HTTP/1.1 only (legacy compatibility)
server := celeris.New(celeris.Config{
    Addr:     ":8080",
    EnableH1: true,
    EnableH2: false,
})

// Both protocols (auto-detect)
server := celeris.NewWithDefaults()  // EnableH1: true, EnableH2: true
```

---

## Response Writer

HTTP/1.1 responses must include proper headers and handle streaming:

```go
type ResponseWriter struct {
    conn        gnet.Conn
    mu          sync.Mutex
    keepAlive   bool
    chunkedMode bool
}

func (w *ResponseWriter) WriteResponse(status int, headers [][2]string, body []byte, endStream bool) error {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    var response bytes.Buffer
    
    // Status line
    response.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", 
        status, http.StatusText(status)))
    
    // Headers
    hasContentLength := false
    for _, h := range headers {
        response.WriteString(fmt.Sprintf("%s: %s\r\n", h[0], h[1]))
        if h[0] == "content-length" {
            hasContentLength = true
        }
    }
    
    // Add Content-Length if not streaming
    if !w.chunkedMode && !hasContentLength && len(body) > 0 {
        response.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
    }
    
    // Connection header
    if w.keepAlive {
        response.WriteString("Connection: keep-alive\r\n")
    } else {
        response.WriteString("Connection: close\r\n")
    }
    
    response.WriteString("\r\n")
    
    // Body
    if len(body) > 0 {
        if w.chunkedMode {
            response.WriteString(fmt.Sprintf("%x\r\n", len(body)))
            response.Write(body)
            response.WriteString("\r\n")
            if endStream {
                response.WriteString("0\r\n\r\n")
            }
        } else {
            response.Write(body)
        }
    }
    
    return w.conn.AsyncWrite(response.Bytes(), nil)
}
```

---

## Testing the Implementation

### HTTP/1.1 with curl

```bash
# Simple request
$ curl -v http://localhost:8080/
> GET / HTTP/1.1
> Host: localhost:8080
> 
< HTTP/1.1 200 OK
< Content-Type: application/json
< Content-Length: 27
< Connection: keep-alive
<
{"message":"Hello, World!"}

# With keep-alive
$ curl -v --http1.1 http://localhost:8080/ http://localhost:8080/health
# Both requests use same connection
```

### HTTP/2 with curl

```bash
$ curl -v --http2-prior-knowledge http://localhost:8080/
> PRI * HTTP/2.0
> 
< HTTP/2 200
< content-type: application/json
< 
{"message":"Hello, World!"}
```

### h2spec Compliance

After adding HTTP/1.1, HTTP/2 compliance must be verified:

```bash
$ make h2spec
Finished in 6.0625 seconds
147 tests, 147 passed, 0 skipped, 0 failed
```

✅ 100% h2spec compliance maintained

---

## Performance Comparison

Both protocols leverage gnet's event-driven architecture:

| Protocol | Simple (RPS) | JSON (RPS) | Params (RPS) |
|----------|--------------|------------|--------------|
| HTTP/1.1 | 75,031 | 74,318 | 77,662 |
| HTTP/2 | 97,728 | 64,134 | 66,092 |

HTTP/2 excels at simple responses (multiplexing advantage), while HTTP/1.1 is competitive for JSON payloads.

---

## Key Takeaways

1. **Detection is cheap**: 4 bytes and a prefix check adds negligible overhead
2. **Unified interfaces simplify handlers**: Convert both protocols to common abstraction
3. **Keep-alive matters for HTTP/1.1**: Connection reuse is critical for performance
4. **Configuration flexibility**: Let users choose protocol support for their use case
5. **Maintain compliance**: Adding HTTP/1.1 must not break HTTP/2

---

## What's Next

In the next article, we focus on performance optimization—there's a lot of low-hanging fruit we haven't picked yet.

---

*Dual-protocol support is working, but there's plenty of room for optimization and cleanup.*
