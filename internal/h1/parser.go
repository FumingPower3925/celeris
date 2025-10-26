// Package h1 provides HTTP/1.1 server implementation using gnet.
package h1

import (
	"bytes"
	"fmt"
	"strconv"
)

// Request represents a parsed HTTP/1.1 request.
type Request struct {
	Method  string
	Path    string
	Version string
	Headers [][2]string
	Host    string
	// Body handling
	ContentLength   int64
	ChunkedEncoding bool
	KeepAlive       bool
	// Parsed state
	HeadersComplete bool
	BodyRead        int64
}

// Reset clears the request fields for reuse.
func (r *Request) Reset() {
	r.Method = ""
	r.Path = ""
	r.Version = ""
	r.Headers = r.Headers[:0]
	r.Host = ""
	r.ContentLength = 0
	r.ChunkedEncoding = false
	r.KeepAlive = false
	r.HeadersComplete = false
	r.BodyRead = 0
}

var (
	bGET    = []byte("GET")
	bHTTP11 = []byte("HTTP/1.1")
	bRoot   = []byte("/")
	bJSON   = []byte("/json")

	sGET    = "GET"
	sHTTP11 = "HTTP/1.1"
	sRoot   = "/"
	sJSON   = "/json"
)

// Parser provides zero-copy HTTP/1.1 request parsing.
type Parser struct {
	buf []byte
	pos int
}

// NewParser creates a new HTTP/1.1 parser.
func NewParser() *Parser {
	return &Parser{}
}

// Reset resets the parser with new buffer data.
func (p *Parser) Reset(buf []byte) {
	p.buf = buf
	p.pos = 0
}

// ParseRequest parses the request line and headers from the buffer.
// Returns the number of bytes consumed, or an error.
func (p *Parser) ParseRequest(req *Request) (int, error) {
	if p.pos >= len(p.buf) {
		return 0, fmt.Errorf("buffer exhausted")
	}

	// Parse request line: METHOD PATH VERSION\r\n
	lineEnd := bytes.Index(p.buf[p.pos:], []byte("\r\n"))
	if lineEnd == -1 {
		// Need more data
		return 0, nil
	}

	line := p.buf[p.pos : p.pos+lineEnd]
	p.pos += lineEnd + 2 // Skip \r\n

	// Split request line into method, path, version
	parts := bytes.SplitN(line, []byte(" "), 3)
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid request line")
	}

	// Intern common method/path/version to avoid allocations
	if bytes.Equal(parts[0], bGET) {
		req.Method = sGET
	} else {
		req.Method = string(parts[0])
	}
	if bytes.Equal(parts[1], bRoot) {
		req.Path = sRoot
	} else if bytes.Equal(parts[1], bJSON) {
		req.Path = sJSON
	} else {
		req.Path = string(parts[1])
	}
	if bytes.Equal(parts[2], bHTTP11) {
		req.Version = sHTTP11
	} else {
		req.Version = string(parts[2])
	}

	// Validate HTTP version
	if req.Version != "HTTP/1.1" && req.Version != "HTTP/1.0" {
		return 0, fmt.Errorf("unsupported HTTP version: %s", req.Version)
	}

	// Parse headers
	req.Headers = make([][2]string, 0, 16)
	req.ContentLength = -1
	req.KeepAlive = req.Version == "HTTP/1.1" // Default keep-alive for HTTP/1.1

	for {
		// Find next line
		lineEnd := bytes.Index(p.buf[p.pos:], []byte("\r\n"))
		if lineEnd == -1 {
			// Need more data
			return 0, nil
		}

		line := p.buf[p.pos : p.pos+lineEnd]
		p.pos += lineEnd + 2 // Skip \r\n

		// Empty line signals end of headers
		if len(line) == 0 {
			req.HeadersComplete = true
			break
		}

		// Parse header: NAME: VALUE
		colonIdx := bytes.IndexByte(line, ':')
		if colonIdx == -1 {
			return 0, fmt.Errorf("invalid header line")
		}

		name := string(bytes.ToLower(bytes.TrimSpace(line[:colonIdx])))
		value := string(bytes.TrimSpace(line[colonIdx+1:]))

		req.Headers = append(req.Headers, [2]string{name, value})

		// Process special headers
		switch name {
		case "host":
			req.Host = value
		case "content-length":
			cl, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid content-length: %w", err)
			}
			req.ContentLength = cl
		case "transfer-encoding":
			if value == "chunked" || bytes.Contains([]byte(value), []byte("chunked")) {
				req.ChunkedEncoding = true
				req.ContentLength = -1 // Chunked overrides Content-Length
			}
		case "connection":
			lowerValue := bytes.ToLower([]byte(value))
			if bytes.Contains(lowerValue, []byte("close")) {
				req.KeepAlive = false
			} else if bytes.Contains(lowerValue, []byte("keep-alive")) {
				req.KeepAlive = true
			}
		}
	}

	// Validate required headers
	if req.Host == "" {
		return 0, fmt.Errorf("missing Host header")
	}

	return p.pos, nil
}

// ParseChunkedBody parses chunked transfer encoding body.
// Returns the decoded chunk data and bytes consumed, or error.
func (p *Parser) ParseChunkedBody() ([]byte, int, error) {
	if p.pos >= len(p.buf) {
		return nil, 0, nil // Need more data
	}

	startPos := p.pos

	// Parse chunk size line: SIZE\r\n
	lineEnd := bytes.Index(p.buf[p.pos:], []byte("\r\n"))
	if lineEnd == -1 {
		return nil, 0, nil // Need more data
	}

	sizeLine := p.buf[p.pos : p.pos+lineEnd]
	p.pos += lineEnd + 2

	// Parse hex size (may have chunk extensions after ;)
	semiIdx := bytes.IndexByte(sizeLine, ';')
	if semiIdx != -1 {
		sizeLine = sizeLine[:semiIdx]
	}

	size, err := strconv.ParseInt(string(bytes.TrimSpace(sizeLine)), 16, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid chunk size: %w", err)
	}

	// Size 0 means last chunk
	if size == 0 {
		// Consume trailing \r\n
		if p.pos+2 <= len(p.buf) {
			p.pos += 2
		}
		return nil, p.pos - startPos, nil
	}

	// Check if we have the full chunk + \r\n
	if p.pos+int(size)+2 > len(p.buf) {
		// Need more data
		p.pos = startPos // Reset position
		return nil, 0, nil
	}

	// Extract chunk data
	chunk := make([]byte, size)
	copy(chunk, p.buf[p.pos:p.pos+int(size)])
	p.pos += int(size) + 2 // Skip chunk data and \r\n

	return chunk, p.pos - startPos, nil
}

// Remaining returns the number of unparsed bytes in the buffer.
func (p *Parser) Remaining() int {
	return len(p.buf) - p.pos
}

// GetBody returns a slice of the body bytes up to content-length.
// This is a zero-copy view into the buffer.
func (p *Parser) GetBody(contentLength int64) []byte {
	if contentLength <= 0 {
		return nil
	}

	available := len(p.buf) - p.pos
	if int64(available) < contentLength {
		// Return what we have
		return p.buf[p.pos:]
	}

	body := p.buf[p.pos : p.pos+int(contentLength)]
	p.pos += int(contentLength)
	return body
}

// ConsumeBody advances the parser position by n bytes (for body consumption).
func (p *Parser) ConsumeBody(n int) {
	p.pos += n
}
