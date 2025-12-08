package celeris

import (
	"bytes"
	"context"
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
	json "github.com/goccy/go-json"
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
	// h1Write is the direct write function for HTTP/1.1 to avoid closure allocation
	h1Write     func(status int, headers [][2]string, body []byte) error
	pushPromise func(streamID uint32, path string, headers [][2]string) error
	values      map[string]interface{}
	hasFlushed  bool
	// streamBuffer accumulates chunks when Flush is called multiple times
	streamBuffer []byte
	// cached pseudo-headers for fast access
	method    string
	path      string
	scheme    string
	authority string
	// Mutex to protect concurrent writes in middleware like Timeout
	writeMu sync.Mutex
	// params stores URL parameters efficiently
	params []RouteParam
}

// RouteParam represents a single URL parameter.
type RouteParam struct {
	Key   string
	Value string
}

var responseBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
var ctxValuesPool = sync.Pool{New: func() any { return make(map[string]interface{}, 8) }}
var paramsPool = sync.Pool{New: func() any { return make([]RouteParam, 0, 4) }}
var contextPool = sync.Pool{New: func() any { return &Context{} }}

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
			// Update indices for remaining headers
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

// Reset clears the headers for reuse.
func (h *Headers) Reset() {
	h.headers = h.headers[:0]
	if h.index != nil {
		for k := range h.index {
			delete(h.index, k)
		}
	}
}

// Reset clears the Context for reuse.
func (c *Context) Reset() {
	c.headers.Reset()
	c.responseHeaders.Reset()
	c.StreamID = 0
	c.body = nil
	c.stream = nil
	c.ctx = nil
	c.writeResponse = nil
	c.pushPromise = nil
	c.hasFlushed = false
	c.method = ""
	c.path = ""
	c.scheme = ""
	c.authority = ""

	if c.responseBody != nil {
		c.responseBody.Reset()
		responseBufPool.Put(c.responseBody)
		c.responseBody = nil
	}

	if len(c.streamBuffer) > 0 {
		c.streamBuffer = c.streamBuffer[:0]
	}

	if c.values != nil {
		for k := range c.values {
			delete(c.values, k)
		}
		ctxValuesPool.Put(c.values)
		c.values = nil
	}

	if len(c.params) > 0 {
		c.params = c.params[:0]
		//nolint:staticcheck // SA6002: params pool stores slices directly, allocation is intentional
		paramsPool.Put(c.params)
		c.params = nil
	}
}

// Release returns the Context to the pool.
func (c *Context) Release() {
	c.Reset()
	contextPool.Put(c)
}

func newContext(ctx context.Context, s *stream.Stream, writeResponse func(uint32, int, [][2]string, []byte) error) *Context {
	c := contextPool.Get().(*Context)
	// Reset is done in Release() before putting back, but safe to double check or just init
	// If we trust Release(), we can skip Reset() here, but for safety let's assume it might be dirty if panic happened
	// Actually, typically Reset is done on Get or Put. Let's do it on Put (Release).
	// So here we just init.
	// But if Release wasn't called (e.g. panic), we might get dirty context if we don't Reset on Get.
	// Safer to Reset on Get.
	// But Release also calls Reset.
	// Let's rely on Release calling Reset, but also ensure fields are overwritten.
	// Actually, c.Reset() clears everything.
	// If we call c.Reset() here, we are safe.
	// But wait, I modified Reset to put things BACK to pool.
	// So if I call Reset() here on a fresh Get(), it's fine because fields are nil.
	// BUT if I call Reset() on a dirty context that wasn't Released, it will put things back to pool.
	// So usage pattern: Get -> Init -> Use -> Release(Reset).
	// If I Reset() here, and it was already Reset() by Release(), it's no-op (checks for nil).

	// But wait, newContext assumes it gets a context.
	// If I call Reset() here, and c.responseBody is nil, it's fine.
	// If c.responseBody is NOT nil (dirty), it puts it back.

	// HOWEVER, c.headers is a struct, not pointer.
	// c.headers.Reset() is fine.

	c.StreamID = s.ID
	c.stream = s
	c.ctx = ctx
	c.writeResponse = writeResponse

	// Init headers (Reset cleared them)
	// headers are struct, so they exist.

	c.body = stream.NewReader(s)
	c.statusCode = 200
	c.responseBody = responseBufPool.Get().(*bytes.Buffer)
	c.responseBody.Reset()

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
		c.headers.Set(name, value)
	})

	return c
}

// NewContextH1 constructs a Context for HTTP/1.1 requests without requiring an HTTP/2 stream.
// It accepts method, path, authority, request headers and an optional body. The write function
// is used to send responses and maps to the underlying transport write path.
func NewContextH1(ctx context.Context, method, path, authority string, reqHeaders [][2]string, body []byte, write func(status int, headers [][2]string, body []byte) error) *Context {
	c := contextPool.Get().(*Context)
	// Assume clean state from Release, but ensure key fields are reset
	c.headers.Reset()
	c.responseHeaders.Reset()
	// Safeguard cleanup if Release wasn't thorough (though it should be)
	if c.values != nil {
		for k := range c.values {
			delete(c.values, k)
		}
		ctxValuesPool.Put(c.values)
		c.values = nil
	}
	if len(c.params) > 0 {
		c.params = c.params[:0]
		//nolint:staticcheck // SA6002: params pool stores slices directly, allocation is intentional
		paramsPool.Put(c.params)
		c.params = nil
	}

	c.StreamID = 1
	c.body = bytes.NewReader(body)
	c.statusCode = 200
	c.responseBody = responseBufPool.Get().(*bytes.Buffer)
	c.responseBody.Reset()
	c.stream = nil
	c.ctx = ctx
	c.writeResponse = func(_ uint32, status int, headers [][2]string, b []byte) error {
		return write(status, headers, b)
	}
	c.pushPromise = nil
	c.hasFlushed = false
	c.method = method
	c.path = path
	c.scheme = "http"
	c.authority = authority

	// Copy request headers
	for _, h := range reqHeaders {
		c.headers.Set(h[0], h[1])
	}
	return c
}

// NewContextH1NoHeaders constructs an H1 Context without copying request headers.
// This is a lighter path for benchmarks and handlers that don't inspect request headers.
func NewContextH1NoHeaders(ctx context.Context, method, path, authority string, body []byte, write func(status int, headers [][2]string, body []byte) error) *Context {
	c := contextPool.Get().(*Context)
	c.headers.Reset()
	c.responseHeaders.Reset()
	if c.values != nil {
		for k := range c.values {
			delete(c.values, k)
		}
		ctxValuesPool.Put(c.values)
		c.values = nil
	}
	if len(c.params) > 0 {
		c.params = c.params[:0]
		//nolint:staticcheck // SA6002: params pool stores slices directly, allocation is intentional
		paramsPool.Put(c.params)
		c.params = nil
	}

	c.StreamID = 1
	// Avoid bytes.NewReader when body is nil (common case)
	if len(body) > 0 {
		c.body = bytes.NewReader(body)
	} else {
		c.body = nil
	}
	c.statusCode = 200
	c.responseBody = responseBufPool.Get().(*bytes.Buffer)
	c.responseBody.Reset()
	c.stream = nil
	c.ctx = ctx
	// Store write function directly to avoid closure allocation
	c.h1Write = write
	c.writeResponse = nil // Not used for H1
	c.pushPromise = nil
	c.hasFlushed = false
	c.method = method
	c.path = path
	c.scheme = "http"
	c.authority = authority

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

// flush sends the response headers and body to the client.
func (c *Context) flush() error {
	// If already flushed, don't flush again (e.g., when ctx.String() already flushed)
	if c.hasFlushed {
		return nil
	}
	var body []byte
	if len(c.streamBuffer) > 0 {
		body = c.streamBuffer
	} else {
		body = c.responseBody.Bytes()
	}
	var err error
	// Fast path for HTTP/1.1: use h1Write directly to avoid closure overhead
	if c.h1Write != nil {
		err = c.h1Write(c.statusCode, c.responseHeaders.All(), body)
	} else if c.writeResponse != nil {
		err = c.writeResponse(c.StreamID, c.statusCode, c.responseHeaders.All(), body)
	} else {
		return fmt.Errorf("no write response function")
	}
	c.responseBody.Reset()
	c.hasFlushed = true
	if len(c.streamBuffer) > 0 {
		c.streamBuffer = c.streamBuffer[:0]
	}
	if c.values != nil {
		for k := range c.values {
			delete(c.values, k)
		}
		c.values = nil
	}
	return err
}

// flushWithBody writes the provided body directly, avoiding copying into responseBody.
// flushWithBody writes the provided body directly to the response.
func (c *Context) flushWithBody(body []byte) error {
	var err error
	// Fast path for HTTP/1.1: use h1Write directly to avoid closure overhead
	if c.h1Write != nil {
		err = c.h1Write(c.statusCode, c.responseHeaders.All(), body)
	} else if c.writeResponse != nil {
		err = c.writeResponse(c.StreamID, c.statusCode, c.responseHeaders.All(), body)
	} else {
		return fmt.Errorf("no write response function")
	}
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

	// Ensure Transfer-Encoding semantics for streaming: no Content-Length once streaming starts
	c.responseHeaders.Del("content-length")

	// Mark underlying stream as streaming to prevent END_STREAM on intermediate chunks
	if c.stream != nil {
		c.stream.IsStreaming = true
	}

	// Send accumulated data immediately
	body := c.responseBody.Bytes()
	err := c.writeResponse(c.StreamID, c.statusCode, c.responseHeaders.All(), body)

	// Reset buffer and mark as flushed
	c.responseBody.Reset()
	c.hasFlushed = true

	// Clear streamBuffer if it was used (legacy support)
	if len(c.streamBuffer) > 0 {
		c.streamBuffer = c.streamBuffer[:0]
	}

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
	for _, p := range c.params {
		if p.Key == name {
			return p.Value
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
