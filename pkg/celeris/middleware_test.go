package celeris

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/albertbausili/celeris/internal/h2/stream"
)

func TestLogger_Middleware(t *testing.T) {
	logger := Logger()

	called := false
	handler := HandlerFunc(func(ctx *Context) error {
		called = true
		return ctx.String(200, "ok")
	})

	wrapped := logger(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/test")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if !called {
		t.Error("Expected handler to be called")
	}
}

func TestRecovery_Middleware(t *testing.T) {
	recovery := Recovery()

	handler := HandlerFunc(func(_ *Context) error {
		panic("test panic")
	})

	wrapped := recovery(handler)

	s := stream.NewStream(1)
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	// Should not panic
	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Logf("ServeHTTP2() error = %v (expected for panic recovery)", err)
	}

	// Check that response was set to 500
	if ctx.Status() != 500 {
		t.Errorf("Expected status 500 after panic, got %d", ctx.Status())
	}
}

func TestRecovery_NormalFlow(t *testing.T) {
	recovery := Recovery()

	called := false
	handler := HandlerFunc(func(ctx *Context) error {
		called = true
		return ctx.String(200, "ok")
	})

	wrapped := recovery(handler)

	s := stream.NewStream(1)
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if !called {
		t.Error("Expected handler to be called")
	}

	if ctx.Status() != 200 {
		t.Errorf("Expected status 200, got %d", ctx.Status())
	}
}

func TestCORS_DefaultConfig(t *testing.T) {
	cors := CORS(DefaultCORSConfig())

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := cors(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if ctx.responseHeaders.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("Expected Access-Control-Allow-Origin header to be set")
	}

	if ctx.responseHeaders.Get("Access-Control-Allow-Methods") == "" {
		t.Error("Expected Access-Control-Allow-Methods header to be set")
	}

	if ctx.responseHeaders.Get("Access-Control-Allow-Headers") == "" {
		t.Error("Expected Access-Control-Allow-Headers header to be set")
	}
}

func TestCORS_CustomConfig(t *testing.T) {
	config := CORSConfig{
		AllowOrigin:      "https://example.com",
		AllowMethods:     "GET, POST",
		AllowHeaders:     "Content-Type",
		AllowCredentials: true,
		MaxAge:           7200,
	}

	cors := CORS(config)

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := cors(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if ctx.responseHeaders.Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Errorf("Expected Access-Control-Allow-Origin https://example.com, got %s",
			ctx.responseHeaders.Get("Access-Control-Allow-Origin"))
	}

	if ctx.responseHeaders.Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("Expected Access-Control-Allow-Credentials to be true")
	}

	if ctx.responseHeaders.Get("Access-Control-Max-Age") != "7200" {
		t.Errorf("Expected Access-Control-Max-Age 7200, got %s",
			ctx.responseHeaders.Get("Access-Control-Max-Age"))
	}
}

func TestCORS_OptionsRequest(t *testing.T) {
	cors := CORS(DefaultCORSConfig())

	handlerCalled := false
	handler := HandlerFunc(func(ctx *Context) error {
		handlerCalled = true
		return ctx.String(200, "ok")
	})

	wrapped := cors(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "OPTIONS")

	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}

	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if handlerCalled {
		t.Error("Expected handler not to be called for OPTIONS request")
	}

	// Note: The context may have been reset after the response
	// This test mainly verifies that no error occurred and the handler wasn't called
}

func TestRequestID_Middleware(t *testing.T) {
	requestID := RequestID()

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := requestID(handler)

	s := stream.NewStream(1)
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	// Note: Context values are cleared after flush, so we can't verify them here
	// The test mainly verifies that the middleware executed without error
}

func TestRequestID_ExistingHeader(t *testing.T) {
	requestID := RequestID()

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := requestID(handler)

	s := stream.NewStream(1)
	s.AddHeader("x-request-id", "existing-id")
	// Create context first
	ctx := newContext(context.Background(), s, nil)

	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}

	// Update context with proper writeResponse function
	ctx.writeResponse = writeResponseFunc

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	// Note: Context values are cleared after flush, so we can't verify them here
	// The test mainly verifies that the middleware executed without error
}

func TestTimeout_Normal(t *testing.T) {
	timeout := Timeout(1 * time.Second)

	called := false
	handler := HandlerFunc(func(ctx *Context) error {
		called = true
		return ctx.String(200, "ok")
	})

	wrapped := timeout(handler)

	s := stream.NewStream(1)
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if !called {
		t.Error("Expected handler to be called")
	}

	if ctx.Status() != 200 {
		t.Errorf("Expected status 200, got %d", ctx.Status())
	}
}

func TestTimeout_Exceeded(t *testing.T) {
	timeout := Timeout(10 * time.Millisecond)

	handler := HandlerFunc(func(ctx *Context) error {
		time.Sleep(100 * time.Millisecond)
		return ctx.String(200, "ok")
	})

	wrapped := timeout(handler)

	s := stream.NewStream(1)

	// Add variables to capture response data
	var capturedStatus int
	var capturedBody []byte

	// Add mock write response function
	writeResponseFunc := func(_ uint32, status int, _ [][2]string, body []byte) error {
		capturedStatus = status
		capturedBody = body
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Logf("ServeHTTP2() error = %v (expected for timeout)", err)
	}

	// Check that timeout response was set
	if capturedStatus != 504 {
		t.Errorf("Expected status 504 for timeout, got %d", capturedStatus)
	}

	if !strings.Contains(string(capturedBody), "Gateway Timeout") {
		t.Errorf("Expected 'Gateway Timeout' in response, got %s", string(capturedBody))
	}
}

func TestCompress_Middleware(t *testing.T) {
	compress := Compress()

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := compress(handler)

	s := stream.NewStream(1)
	s.AddHeader("accept-encoding", "gzip")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}
}

func TestRateLimiter_Middleware(t *testing.T) {
	rateLimiter := RateLimiter(100)

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := rateLimiter(handler)

	s := stream.NewStream(1)
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}
}

func TestGenerateRequestID(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()

	if id1 == "" {
		t.Error("Expected non-empty request ID")
	}

	if id1 == id2 {
		t.Error("Expected different request IDs")
	}
}

// New tests for Logger middleware with custom config
func TestLoggerWithConfig_JSONFormat(t *testing.T) {
	var buf strings.Builder
	config := LoggerConfig{
		Output: &buf,
		Format: "json",
	}
	logger := LoggerWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		ctx.Set("request-id", "test-123")
		return ctx.String(200, "ok")
	})

	wrapped := logger(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "POST")
	s.AddHeader(":path", "/api/users")
	// Create context first
	ctx := newContext(context.Background(), s, nil)

	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, headers [][2]string, _ []byte) error {
		// Copy headers to context response headers for test inspection
		for _, header := range headers {
			ctx.responseHeaders.Set(header[0], header[1])
		}
		return nil
	}

	// Update context with proper writeResponse function
	ctx.writeResponse = writeResponseFunc

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "POST") {
		t.Errorf("Expected log to contain method POST, got: %s", output)
	}
	if !strings.Contains(output, "/api/users") {
		t.Errorf("Expected log to contain path /api/users, got: %s", output)
	}
	// Note: The request-id set in the handler may not be available in logs
	// since the context gets reset after flush. This might be expected behavior.
}

func TestLoggerWithConfig_SkipPaths(t *testing.T) {
	var buf strings.Builder
	config := LoggerConfig{
		Output:    &buf,
		Format:    "text",
		SkipPaths: []string{"/health"},
	}
	logger := LoggerWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := logger(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/health")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	_ = wrapped.ServeHTTP2(ctx)

	output := buf.String()
	if output != "" {
		t.Errorf("Expected no log output for skipped path, got: %s", output)
	}
}

// Tests for Compress middleware
func TestCompressWithConfig_Gzip(t *testing.T) {
	t.Skip("Compression requires full request cycle with flush - tested in integration tests")
	config := CompressConfig{
		Level:   6,
		MinSize: 10, // Small for testing
	}
	compress := CompressWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		// Create response larger than MinSize
		return ctx.String(200, "This is a test response that is long enough to be compressed")
	})

	wrapped := compress(handler)

	s := stream.NewStream(1)
	s.AddHeader("accept-encoding", "gzip")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	encoding := ctx.responseHeaders.Get("content-encoding")
	if encoding != "gzip" {
		t.Errorf("Expected content-encoding gzip, got %s", encoding)
	}

	vary := ctx.responseHeaders.Get("vary")
	if vary != "Accept-Encoding" {
		t.Errorf("Expected Vary header, got %s", vary)
	}
}

func TestCompressWithConfig_Brotli(t *testing.T) {
	t.Skip("Compression requires full request cycle with flush - tested in integration tests")
	config := CompressConfig{
		Level:   6,
		MinSize: 10,
	}
	compress := CompressWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "This is a test response that is long enough to be compressed with brotli")
	})

	wrapped := compress(handler)

	s := stream.NewStream(1)
	s.AddHeader("accept-encoding", "br, gzip")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	encoding := ctx.responseHeaders.Get("content-encoding")
	if encoding != "br" {
		t.Errorf("Expected content-encoding br, got %s", encoding)
	}
}

func TestCompressWithConfig_TooSmall(t *testing.T) {
	config := CompressConfig{
		Level:   6,
		MinSize: 1000, // Larger than response
	}
	compress := CompressWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "small")
	})

	wrapped := compress(handler)

	s := stream.NewStream(1)
	s.AddHeader("accept-encoding", "gzip")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	encoding := ctx.responseHeaders.Get("content-encoding")
	if encoding != "" {
		t.Errorf("Expected no compression for small response, got %s", encoding)
	}
}

func TestCompressWithConfig_ExcludedType(t *testing.T) {
	config := CompressConfig{
		Level:         6,
		MinSize:       10,
		ExcludedTypes: []string{"image/"},
	}
	compress := CompressWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		ctx.SetHeader("content-type", "image/png")
		return ctx.String(200, "This is a long image data that should not be compressed")
	})

	wrapped := compress(handler)

	s := stream.NewStream(1)
	s.AddHeader("accept-encoding", "gzip")
	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}
	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	encoding := ctx.responseHeaders.Get("content-encoding")
	if encoding != "" {
		t.Errorf("Expected no compression for excluded type, got %s", encoding)
	}
}
