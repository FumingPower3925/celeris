// Package h1 provides HTTP/1.1 server implementation using gnet.
package h1

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Request represents a parsed HTTP/1.1 request.
type Request struct {
	Method  string
	Path    string
	Version string
	Headers [][2]string
	// RawHeaders holds zero-copy views (name,value) into the parse buffer.
	// Names/values are exactly as received on the wire (no lowercasing).
	// These slices are only valid during request handling while the buffer lives.
	RawHeaders [][2][]byte
	Host       string
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
	r.RawHeaders = r.RawHeaders[:0]
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
	// noStringHeaders skips building req.Headers strings; only RawHeaders and
	// special fields (Host, ContentLength, KeepAlive, ChunkedEncoding) are set.
	noStringHeaders bool
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

	// Request line
	complete, err := p.parseRequestLine(req)
	if err != nil {
		return 0, err
	}
	if !complete {
		return 0, nil
	}

	// Pre-allocate/prepare header containers
	if p.noStringHeaders {
		req.Headers = req.Headers[:0]
	} else {
		if cap(req.Headers) >= 16 {
			req.Headers = req.Headers[:0]
		} else {
			req.Headers = make([][2]string, 0, 16)
		}
	}
	req.ContentLength = -1
	req.KeepAlive = req.Version == "HTTP/1.1"

	// Headers
	complete, err = p.parseHeaders(req)
	if err != nil {
		return 0, err
	}
	if !complete {
		return 0, nil
	}

	if req.Host == "" {
		return 0, fmt.Errorf("missing Host header")
	}
	return p.pos, nil
}

// parseRequestLine parses METHOD SP PATH SP VERSION CRLF, advancing p.pos.
// Returns complete=false if more data is needed.
func (p *Parser) parseRequestLine(req *Request) (bool, error) {
	lineEnd := bytes.Index(p.buf[p.pos:], []byte("\r\n"))
	if lineEnd == -1 {
		return false, nil
	}
	line := p.buf[p.pos : p.pos+lineEnd]
	p.pos += lineEnd + 2

	parts := bytes.SplitN(line, []byte(" "), 3)
	if len(parts) != 3 {
		return false, fmt.Errorf("invalid request line")
	}
	if bytes.Equal(parts[0], bGET) {
		req.Method = sGET
	} else {
		req.Method = string(parts[0])
	}
	switch {
	case bytes.Equal(parts[1], bRoot):
		req.Path = sRoot
	case bytes.Equal(parts[1], bJSON):
		req.Path = sJSON
	default:
		req.Path = string(parts[1])
	}
	if bytes.Equal(parts[2], bHTTP11) {
		req.Version = sHTTP11
	} else {
		req.Version = string(parts[2])
	}
	if req.Version != "HTTP/1.1" && req.Version != "HTTP/1.0" {
		return false, fmt.Errorf("unsupported HTTP version: %s", req.Version)
	}
	return true, nil
}

// parseHeaders parses headers until CRLF CRLF, advancing p.pos.
// Returns complete=false if more data is needed.
func (p *Parser) parseHeaders(req *Request) (bool, error) {
	for {
		lineEnd := bytes.Index(p.buf[p.pos:], []byte("\r\n"))
		if lineEnd == -1 {
			return false, nil
		}
		line := p.buf[p.pos : p.pos+lineEnd]
		p.pos += lineEnd + 2
		if len(line) == 0 {
			req.HeadersComplete = true
			return true, nil
		}
		colonIdx := bytes.IndexByte(line, ':')
		if colonIdx == -1 {
			return false, fmt.Errorf("invalid header line")
		}
		rawName := bytes.TrimSpace(line[:colonIdx])
		rawValue := bytes.TrimSpace(line[colonIdx+1:])
		if err := p.appendHeader(req, rawName, rawValue); err != nil {
			return false, err
		}
	}
}

// appendHeader processes a single header line given raw name and value.
func (p *Parser) appendHeader(req *Request, rawName, rawValue []byte) error {
	// Always track raw header view
	req.RawHeaders = append(req.RawHeaders, [2][]byte{rawName, rawValue})
	if !p.noStringHeaders {
		var name string
		switch {
		case asciiEqualFold(rawName, "Host"):
			name = "host"
		case asciiEqualFold(rawName, "Content-Length"):
			name = "content-length"
		case asciiEqualFold(rawName, "Transfer-Encoding"):
			name = "transfer-encoding"
		case asciiEqualFold(rawName, "Connection"):
			name = "connection"
		default:
			name = strings.ToLower(string(rawName))
		}
		value := string(rawValue)
		req.Headers = append(req.Headers, [2]string{name, value})
		switch name {
		case "host":
			req.Host = value
		case "content-length":
			cl, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid content-length: %w", err)
			}
			req.ContentLength = cl
		case "transfer-encoding":
			if asciiContainsFoldString(value, "chunked") {
				req.ChunkedEncoding = true
				req.ContentLength = -1
			}
		case "connection":
			if asciiContainsFoldString(value, "close") {
				req.KeepAlive = false
			} else if asciiContainsFoldString(value, "keep-alive") {
				req.KeepAlive = true
			}
		}
		return nil
	}
	// Zero-alloc mode: set special fields only
	if asciiEqualFold(rawName, "Host") {
		req.Host = string(rawValue)
		return nil
	}
	if asciiEqualFold(rawName, "Content-Length") {
		if cl, ok := parseInt64Bytes(rawValue); ok {
			req.ContentLength = cl
		} else {
			return fmt.Errorf("invalid content-length")
		}
		return nil
	}
	if asciiEqualFold(rawName, "Transfer-Encoding") {
		if asciiContainsFoldBytes(rawValue, "chunked") {
			req.ChunkedEncoding = true
			req.ContentLength = -1
		}
		return nil
	}
	if asciiEqualFold(rawName, "Connection") {
		if asciiContainsFoldBytes(rawValue, "close") {
			req.KeepAlive = false
		} else if asciiContainsFoldBytes(rawValue, "keep-alive") {
			req.KeepAlive = true
		}
		return nil
	}
	return nil
}

// asciiEqualFold reports whether b equals s under ASCII case-insensitive comparison
func asciiEqualFold(b []byte, s string) bool {
	if len(b) != len(s) {
		return false
	}
	for i := 0; i < len(b); i++ {
		cb := b[i]
		cs := s[i]
		// to lower ASCII
		if 'A' <= cb && cb <= 'Z' {
			cb |= 0x20
		}
		if 'A' <= cs && cs <= 'Z' {
			cs |= 0x20
		}
		if cb != cs {
			return false
		}
	}
	return true
}

// asciiContainsFoldString reports whether s contains sub under ASCII case-insensitive comparison
func asciiContainsFoldString(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	// naive scan with ASCII fold
	n := len(s)
	m := len(sub)
	if m > n {
		return false
	}
	for i := 0; i <= n-m; i++ {
		match := true
		for j := 0; j < m; j++ {
			cs := s[i+j]
			ct := sub[j]
			if 'A' <= cs && cs <= 'Z' {
				cs |= 0x20
			}
			if 'A' <= ct && ct <= 'Z' {
				ct |= 0x20
			}
			if cs != ct {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// asciiContainsFoldBytes reports whether b contains sub (ASCII case-insensitive)
func asciiContainsFoldBytes(b []byte, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	m := len(sub)
	if m > len(b) {
		return false
	}
	for i := 0; i <= len(b)-m; i++ {
		match := true
		for j := 0; j < m; j++ {
			cb := b[i+j]
			cs := sub[j]
			if 'A' <= cb && cb <= 'Z' {
				cb |= 0x20
			}
			if 'A' <= cs && cs <= 'Z' {
				cs |= 0x20
			}
			if cb != cs {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// parseInt64Bytes parses a base-10 int64 from ASCII bytes, returning ok=false on error
func parseInt64Bytes(b []byte) (int64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	var n int64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
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
