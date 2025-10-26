package celeris

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/albertbausili/celeris/internal/h2/stream"
)

// Context represents an HTTP/2 request-response context.
type Context struct {
	StreamID        uint32
	headers         Headers
	body            io.Reader
	statusCode      int
	responseHeaders Headers
	responseBody    *bytes.Buffer
	stream          *stream.Stream
	ctx             context.Context
	writeResponse   func(streamID uint32, status int, headers [][2]string, body []byte) error
	pushPromise     func(streamID uint32, path string, headers [][2]string) error
	values          map[string]interface{}
	hasFlushed      bool
	// cached pseudo-headers for fast access
	method    string
	path      string
	scheme    string
	authority string
	// Mutex to protect concurrent writes in middleware like Timeout
	writeMu sync.Mutex
}

var responseBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
var ctxValuesPool = sync.Pool{New: func() any { return make(map[string]interface{}, 8) }}

// Headers represents HTTP headers with efficient access.
type Headers struct {
	headers [][2]string
	index   map[string]int
}

// NewHeaders creates a new Headers instance.
func NewHeaders() Headers {
	return Headers{
		headers: make([][2]string, 0),
		// index is allocated lazily on first Set to avoid per-request map alloc
		index: nil,
	}
}

// Set sets a header value, replacing any existing value.
// Keys are automatically converted to lowercase per HTTP/2 spec (RFC 7540).
func (h *Headers) Set(key, value string) {
	lowerKey := strings.ToLower(key)
	// Lazily build index on first set if nil
	if h.index == nil {
		h.index = make(map[string]int, len(h.headers)+2)
		for i := range h.headers {
			h.index[h.headers[i][0]] = i
		}
	}
	if idx, ok := h.index[lowerKey]; ok {
		h.headers[idx][1] = value
		return
	}
	h.index[lowerKey] = len(h.headers)
	h.headers = append(h.headers, [2]string{lowerKey, value})
}

// Get retrieves a header value by key.
// Key lookup is case-insensitive per HTTP/2 spec (RFC 7540).
func (h *Headers) Get(key string) string {
	lowerKey := strings.ToLower(key)
	if h.index != nil {
		if idx, ok := h.index[lowerKey]; ok {
			return h.headers[idx][1]
		}
		return ""
	}
	// No index map yet: linear search
	for i := range h.headers {
		if h.headers[i][0] == lowerKey {
			return h.headers[i][1]
		}
	}
	return ""
}

// Del removes a header by key.
// Key lookup is case-insensitive per HTTP/2 spec (RFC 7540).
func (h *Headers) Del(key string) {
	lowerKey := strings.ToLower(key)
	if h.index != nil {
		if idx, ok := h.index[lowerKey]; ok {
			h.headers = append(h.headers[:idx], h.headers[idx+1:]...)
			delete(h.index, lowerKey)
			for i := idx; i < len(h.headers); i++ {
				h.index[h.headers[i][0]] = i
			}
		}
		return
	}
	// No index map yet: linear removal
	for i := range h.headers {
		if h.headers[i][0] == lowerKey {
			h.headers = append(h.headers[:i], h.headers[i+1:]...)
			break
		}
	}
}

// All returns all headers as a slice of key-value pairs.
func (h *Headers) All() [][2]string {
	return h.headers
}

// Has checks if a header exists.
// Key lookup is case-insensitive per HTTP/2 spec (RFC 7540).
func (h *Headers) Has(key string) bool {
	lowerKey := strings.ToLower(key)
	if h.index != nil {
		_, ok := h.index[lowerKey]
		return ok
	}
	for i := range h.headers {
		if h.headers[i][0] == lowerKey {
			return true
		}
	}
	return false
}

func newContext(ctx context.Context, s *stream.Stream, writeResponse func(uint32, int, [][2]string, []byte) error) *Context {
	c := &Context{
		StreamID:        s.ID,
		headers:         NewHeaders(),
		body:            stream.NewReader(s),
		statusCode:      200,
		responseHeaders: NewHeaders(),
		responseBody:    responseBufPool.Get().(*bytes.Buffer),
		stream:          s,
		ctx:             ctx,
		writeResponse:   writeResponse,
		// Lazily allocate values map to avoid cost on simple paths
		values: nil,
	}

	// Populate headers directly from stream
	s.ForEachHeader(func(name, value string) {
		// Cache selected pseudo-headers for fast access
		switch name {
		case ":method":
			c.method = value
		case ":path":
			c.path = value
		case ":scheme":
			c.scheme = value
		case ":authority":
			c.authority = value
		}
		// Store all non-pseudo headers; also store pseudo-headers for completeness
		// Header struct normalizes to lowercase
		c.headers.Set(name, value)
	})

	return c
}

// NewContextH1 constructs a Context for HTTP/1.1 requests without requiring an HTTP/2 stream.
// It accepts method, path, authority, request headers and an optional body. The write function
// is used to send responses and maps to the underlying transport write path.
func NewContextH1(ctx context.Context, method, path, authority string, reqHeaders [][2]string, body []byte, write func(status int, headers [][2]string, body []byte) error) *Context {
	c := &Context{
		StreamID:        1,
		headers:         NewHeaders(),
		body:            bytes.NewReader(body),
		statusCode:      200,
		responseHeaders: NewHeaders(),
		responseBody:    responseBufPool.Get().(*bytes.Buffer),
		stream:          nil,
		ctx:             ctx,
		writeResponse: func(_ uint32, status int, headers [][2]string, b []byte) error {
			return write(status, headers, b)
		},
		values:    nil,
		method:    method,
		path:      path,
		scheme:    "http",
		authority: authority,
	}
	// Copy request headers
	for _, h := range reqHeaders {
		c.headers.Set(h[0], h[1])
	}
	return c
}

// NewContextH1NoHeaders constructs an H1 Context without copying request headers.
// This is a lighter path for benchmarks and handlers that don't inspect request headers.
func NewContextH1NoHeaders(ctx context.Context, method, path, authority string, body []byte, write func(status int, headers [][2]string, body []byte) error) *Context {
	c := &Context{
		StreamID:        1,
		headers:         NewHeaders(),
		body:            bytes.NewReader(body),
		statusCode:      200,
		responseHeaders: NewHeaders(),
		responseBody:    responseBufPool.Get().(*bytes.Buffer),
		stream:          nil,
		ctx:             ctx,
		writeResponse: func(_ uint32, status int, headers [][2]string, b []byte) error {
			return write(status, headers, b)
		},
		values:    nil,
		method:    method,
		path:      path,
		scheme:    "http",
		authority: authority,
	}
	return c
}

// Method returns the HTTP request method.
func (c *Context) Method() string {
	if c.method != "" {
		return c.method
	}
	return c.headers.Get(":method")
}

// Path returns the HTTP request path.
func (c *Context) Path() string {
	if c.path != "" {
		return c.path
	}
	return c.headers.Get(":path")
}

// Scheme returns the HTTP request scheme (http or https).
func (c *Context) Scheme() string {
	if c.scheme != "" {
		return c.scheme
	}
	return c.headers.Get(":scheme")
}

// Authority returns the HTTP request authority (host).
func (c *Context) Authority() string {
	if c.authority != "" {
		return c.authority
	}
	return c.headers.Get(":authority")
}

// Header returns the request headers.
func (c *Context) Header() *Headers {
	return &c.headers
}

// Body returns the request body reader.
func (c *Context) Body() io.Reader {
	return c.body
}

// SetStatus sets the HTTP response status code.
func (c *Context) SetStatus(code int) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.statusCode = code
}

// Status returns the current HTTP response status code.
func (c *Context) Status() int {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.statusCode
}

// SetHeader sets an HTTP response header.
// Header names are automatically converted to lowercase per HTTP/2 spec (RFC 7540).
func (c *Context) SetHeader(key, value string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.responseHeaders.Set(key, value)
}

// Write writes data to the response body.
func (c *Context) Write(data []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.responseBody.Write(data)
}

// WriteString writes a string to the response body.
func (c *Context) WriteString(s string) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.responseBody.WriteString(s)
}

// JSON sends a JSON response with the given status code.
func (c *Context) JSON(status int, v interface{}) error {
	c.writeMu.Lock()
	c.statusCode = status
	// Avoid map allocation: append headers directly
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-type", "application/json"})
	data, err := json.Marshal(v)
	if err != nil {
		c.writeMu.Unlock()
		return err
	}
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-length", strconv.Itoa(len(data))})
	_, err = c.responseBody.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		return err
	}
	return c.flush()
}

// String sends a formatted text response with the given status code.
func (c *Context) String(status int, format string, values ...interface{}) error {
	c.writeMu.Lock()
	c.statusCode = status
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-type", "text/plain; charset=utf-8"})
	s := fmt.Sprintf(format, values...)
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-length", strconv.Itoa(len(s))})
	c.writeMu.Unlock()
	return c.flushWithBody([]byte(s))
}

// HTML sends an HTML response with the given status code.
func (c *Context) HTML(status int, html string) error {
	c.writeMu.Lock()
	c.statusCode = status
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-type", "text/html; charset=utf-8"})
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-length", strconv.Itoa(len(html))})
	c.writeMu.Unlock()
	return c.flushWithBody([]byte(html))
}

// Data sends a response with custom content type and data.
func (c *Context) Data(status int, contentType string, data []byte) error {
	c.writeMu.Lock()
	c.statusCode = status
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-type", contentType})
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-length", strconv.Itoa(len(data))})
	c.writeMu.Unlock()
	return c.flushWithBody(data)
}

// Plain sends a plain text response without fmt formatting overhead.
func (c *Context) Plain(status int, s string) error {
	c.writeMu.Lock()
	c.statusCode = status
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-type", "text/plain; charset=utf-8"})
	c.responseHeaders.headers = append(c.responseHeaders.headers, [2]string{"content-length", strconv.Itoa(len(s))})
	c.writeMu.Unlock()
	return c.flushWithBody([]byte(s))
}

// NoContent sends a response with no body content.
func (c *Context) NoContent(status int) error {
	c.SetStatus(status)
	return c.flush()
}

// Redirect sends an HTTP redirect response.
func (c *Context) Redirect(status int, url string) error {
	if status < 300 || status > 308 {
		status = 302
	}
	c.SetStatus(status)
	c.SetHeader("location", url)
	return c.flush()
}

func (c *Context) flush() error {
	if c.writeResponse == nil {
		return fmt.Errorf("no write response function")
	}
	err := c.writeResponse(c.StreamID, c.statusCode, c.responseHeaders.All(), c.responseBody.Bytes())
	c.responseBody.Reset()
	c.hasFlushed = true
	if c.values != nil {
		for k := range c.values {
			delete(c.values, k)
		}
		c.values = nil
	}
	return err
}

// flushWithBody writes the provided body directly, avoiding copying into responseBody.
func (c *Context) flushWithBody(body []byte) error {
	if c.writeResponse == nil {
		return fmt.Errorf("no write response function")
	}
	err := c.writeResponse(c.StreamID, c.statusCode, c.responseHeaders.All(), body)
	c.responseBody.Reset()
	c.hasFlushed = true
	if c.values != nil {
		for k := range c.values {
			delete(c.values, k)
		}
		c.values = nil
	}
	return err
}

// Context returns the underlying context.Context.
func (c *Context) Context() context.Context {
	return c.ctx
}

// Set stores a key-value pair in the context.
func (c *Context) Set(key string, value interface{}) {
	if c.values == nil {
		if v := ctxValuesPool.Get(); v != nil {
			c.values = v.(map[string]interface{})
		} else {
			c.values = make(map[string]interface{}, 8)
		}
	}
	c.values[key] = value
}

// Get retrieves a value from the context by key.
func (c *Context) Get(key string) (interface{}, bool) {
	if c.values == nil {
		return nil, false
	}
	val, ok := c.values[key]
	return val, ok
}

// MustGet retrieves a value from the context by key, panicking if not found.
func (c *Context) MustGet(key string) interface{} {
	if val, ok := c.Get(key); ok {
		return val
	}
	panic(fmt.Sprintf("key %q not found in context", key))
}

// BodyBytes reads and returns the entire request body as bytes.
func (c *Context) BodyBytes() ([]byte, error) {
	return io.ReadAll(c.body)
}

// BindJSON parses the request body as JSON into the provided value.
func (c *Context) BindJSON(v interface{}) error {
	data, err := c.BodyBytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// PushPromise sends an HTTP/2 server push promise for the given resource.
func (c *Context) PushPromise(path string, headers map[string]string) error {
	if c.pushPromise == nil {
		return fmt.Errorf("server push not supported")
	}

	pushHeaders := make([][2]string, 0, len(headers)+3)

	pushHeaders = append(pushHeaders, [2]string{":method", "GET"})
	pushHeaders = append(pushHeaders, [2]string{":path", path})
	pushHeaders = append(pushHeaders, [2]string{":scheme", c.Scheme()})

	for k, v := range headers {
		pushHeaders = append(pushHeaders, [2]string{k, v})
	}

	return c.pushPromise(c.StreamID, path, pushHeaders)
}

// Flush sends the current response headers and body, then resets the body buffer.
// This allows for streaming responses by calling Flush multiple times.
func (c *Context) Flush() error {
	if c.writeResponse == nil {
		return fmt.Errorf("no write response function")
	}
	// Copy current buffer to avoid losing previous chunks when reusing buffer
	data := append([]byte(nil), c.responseBody.Bytes()...)
	// Ensure Transfer-Encoding semantics for streaming: no Content-Length once streaming starts
	// Remove content-length header to prevent peers from expecting a fixed length
	c.responseHeaders.Del("content-length")
	// Mark underlying stream as streaming to prevent END_STREAM on intermediate chunks
	if c.stream != nil {
		c.stream.IsStreaming = true
	}
	err := c.writeResponse(c.StreamID, c.statusCode, c.responseHeaders.All(), data)
	c.responseBody.Reset()
	c.hasFlushed = true
	return err
}

// Stream allows streaming responses by calling the provided function with a writer.
// The function should write chunks and return when done.
func (c *Context) Stream(fn func(w io.Writer) error) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return fn(c.responseBody)
}

// SSEEvent represents a Server-Sent Event.
type SSEEvent struct {
	ID    string
	Event string
	Data  string
	Retry int
}

// SSE sends a Server-Sent Event with proper formatting.
func (c *Context) SSE(event SSEEvent) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	// Set SSE headers if not already set
	if c.responseHeaders.Get("content-type") == "" {
		c.responseHeaders.Set("content-type", "text/event-stream")
		c.responseHeaders.Set("cache-control", "no-cache")
		c.responseHeaders.Set("connection", "keep-alive")
	}

	// Write SSE format
	if event.ID != "" {
		fmt.Fprintf(c.responseBody, "id: %s\n", event.ID)
	}
	if event.Event != "" {
		fmt.Fprintf(c.responseBody, "event: %s\n", event.Event)
	}
	if event.Retry > 0 {
		fmt.Fprintf(c.responseBody, "retry: %d\n", event.Retry)
	}

	// Write data (support multi-line)
	lines := strings.Split(event.Data, "\n")
	for _, line := range lines {
		fmt.Fprintf(c.responseBody, "data: %s\n", line)
	}

	// End event with double newline
	fmt.Fprint(c.responseBody, "\n")

	return nil
}

// Writer returns the underlying response writer for advanced streaming use cases.
func (c *Context) Writer() io.Writer {
	return c.responseBody
}

// Query returns the query parameter value for the given key.
func (c *Context) Query(key string) string {
	path := c.Path()
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		query := path[idx+1:]
		return parseQuery(query, key)
	}
	return ""
}

// QueryDefault returns the query parameter value or a default if not found.
func (c *Context) QueryDefault(key, defaultValue string) string {
	if value := c.Query(key); value != "" {
		return value
	}
	return defaultValue
}

// QueryInt returns the query parameter value as an integer.
func (c *Context) QueryInt(key string) (int, error) {
	value := c.Query(key)
	if value == "" {
		return 0, fmt.Errorf("query parameter %q not found", key)
	}
	return strconv.Atoi(value)
}

// QueryBool returns the query parameter value as a boolean.
func (c *Context) QueryBool(key string) bool {
	value := c.Query(key)
	b, _ := strconv.ParseBool(value)
	return b
}

// parseQuery extracts a query parameter value from a query string.
func parseQuery(query, key string) string {
	for len(query) > 0 {
		// Find next &
		end := strings.IndexByte(query, '&')
		if end == -1 {
			end = len(query)
		}

		pair := query[:end]
		query = query[end:]
		if len(query) > 0 {
			query = query[1:] // skip &
		}

		// Parse key=value
		eq := strings.IndexByte(pair, '=')
		if eq == -1 {
			continue
		}

		if pair[:eq] == key {
			value, _ := url.QueryUnescape(pair[eq+1:])
			return value
		}
	}
	return ""
}

// Cookie returns the value of the cookie with the given name.
func (c *Context) Cookie(name string) string {
	cookieHeader := c.Header().Get("cookie")
	if cookieHeader == "" {
		return ""
	}

	// Parse cookie header
	cookies := strings.Split(cookieHeader, ";")
	for _, cookie := range cookies {
		cookie = strings.TrimSpace(cookie)
		parts := strings.SplitN(cookie, "=", 2)
		if len(parts) == 2 && parts[0] == name {
			value, _ := url.QueryUnescape(parts[1])
			return value
		}
	}
	return ""
}

// SetCookie adds a Set-Cookie header to the response.
func (c *Context) SetCookie(cookie *http.Cookie) {
	c.SetHeader("set-cookie", cookie.String())
}

// FormValue returns the value of the form field with the given key.
// This reads from the request body (for POST/PUT with application/x-www-form-urlencoded).
func (c *Context) FormValue(key string) (string, error) {
	contentType := c.Header().Get("content-type")
	if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		return "", fmt.Errorf("content-type is not application/x-www-form-urlencoded")
	}

	body, err := c.BodyBytes()
	if err != nil {
		return "", err
	}

	return parseQuery(string(body), key), nil
}

// Param returns the value of the URL parameter (from router).
func (c *Context) Param(name string) string {
	if val, ok := c.Get(name); ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// File sends a file as response with proper content type and caching headers.
func (c *Context) File(filepath string) error {
	// Open file
	file, err := os.Open(filepath) // #nosec G304 - File path is validated by caller
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	// Get file info
	info, err := file.Stat()
	if err != nil {
		return err
	}

	// Check if it's a directory
	if info.IsDir() {
		return fmt.Errorf("cannot serve directory")
	}

	// Set content type based on file extension
	ext := path.Ext(filepath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.SetHeader("content-type", contentType)

	// Set Last-Modified header
	modTime := info.ModTime().UTC().Format(http.TimeFormat)
	c.SetHeader("last-modified", modTime)

	// Generate ETag based on mod time and size
	etag := fmt.Sprintf(`"%x-%x"`, info.ModTime().Unix(), info.Size())
	c.SetHeader("etag", etag)

	// Check If-None-Match (ETag)
	if c.Header().Get("if-none-match") == etag {
		return c.NoContent(304)
	}

	// Check If-Modified-Since
	if ifModSince := c.Header().Get("if-modified-since"); ifModSince != "" {
		if t, err := http.ParseTime(ifModSince); err == nil {
			if !info.ModTime().After(t) {
				return c.NoContent(304)
			}
		}
	}

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	c.SetStatus(200)
	_, err = c.Write(content)
	return err
}

// Attachment sends a file as an attachment with the specified filename.
func (c *Context) Attachment(filename, filepath string) error {
	c.SetHeader("content-disposition", fmt.Sprintf("attachment; filename=%q", filename))
	return c.File(filepath)
}
