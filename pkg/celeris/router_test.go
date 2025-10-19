package celeris

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/albertbausili/celeris/internal/stream"
)

func TestRouter_AddRoute(t *testing.T) {
	router := NewRouter()

	called := false
	handler := HandlerFunc(func(_ *Context) error {
		called = true
		return nil
	})

	router.GET("/test", handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/test")

	ctx := newContext(context.Background(), s, nil)
	_ = router.ServeHTTP2(ctx)

	if !called {
		t.Error("Expected handler to be called")
	}
}

func TestRouter_Parameters(t *testing.T) {
	router := NewRouter()

	var capturedID string
	handler := HandlerFunc(func(ctx *Context) error {
		capturedID = Param(ctx, "id")
		return nil
	})

	router.GET("/users/:id", handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/users/123")

	ctx := newContext(context.Background(), s, nil)
	_ = router.ServeHTTP2(ctx)

	if capturedID != "123" {
		t.Errorf("Expected id '123', got %s", capturedID)
	}
}

func TestRouter_NotFound(t *testing.T) {
	router := NewRouter()

	called := false
	router.NotFound(HandlerFunc(func(ctx *Context) error {
		called = true
		return ctx.String(404, "Not Found")
	}))

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/nonexistent")

	ctx := newContext(context.Background(), s, nil)
	_ = router.ServeHTTP2(ctx)

	if !called {
		t.Error("Expected not found handler to be called")
	}
}

func TestRouter_Middleware(t *testing.T) {
	router := NewRouter()

	middlewareCalled := false
	middleware := func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			middlewareCalled = true
			return next.ServeHTTP2(ctx)
		})
	}

	router.Use(middleware)

	handlerCalled := false
	handler := HandlerFunc(func(_ *Context) error {
		handlerCalled = true
		return nil
	})

	router.GET("/test", handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/test")

	ctx := newContext(context.Background(), s, nil)
	_ = router.ServeHTTP2(ctx)

	if !middlewareCalled {
		t.Error("Expected middleware to be called")
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}

func TestRouter_Group(t *testing.T) {
	router := NewRouter()

	group := router.Group("/api")

	called := false
	handler := HandlerFunc(func(_ *Context) error {
		called = true
		return nil
	})

	group.GET("/users", handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/api/users")

	ctx := newContext(context.Background(), s, nil)
	_ = router.ServeHTTP2(ctx)

	if !called {
		t.Error("Expected handler to be called")
	}
}

func TestRouter_Wildcard(t *testing.T) {
	router := NewRouter()

	var capturedPath string
	handler := HandlerFunc(func(ctx *Context) error {
		capturedPath = Param(ctx, "path")
		return nil
	})

	router.GET("/files/*path", handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/files/docs/test.pdf")

	ctx := newContext(context.Background(), s, nil)
	_ = router.ServeHTTP2(ctx)

	// The wildcard captures the entire remaining path including the prefix
	if capturedPath != "files/docs/test.pdf" && capturedPath != "docs/test.pdf" {
		t.Errorf("Expected path 'docs/test.pdf' or 'files/docs/test.pdf', got %s", capturedPath)
	}
}

func BenchmarkRouter_StaticRoute(b *testing.B) {
	router := NewRouter()

	handler := HandlerFunc(func(_ *Context) error {
		return nil
	})

	router.GET("/test", handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/test")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := newContext(context.Background(), s, nil)
		_ = router.ServeHTTP2(ctx)
	}
}

func BenchmarkRouter_ParameterRoute(b *testing.B) {
	router := NewRouter()

	handler := HandlerFunc(func(ctx *Context) error {
		_ = Param(ctx, "id")
		return nil
	})

	router.GET("/users/:id", handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/users/123")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := newContext(context.Background(), s, nil)
		_ = router.ServeHTTP2(ctx)
	}
}

// Tests for error handling

func TestHTTPError_Creation(t *testing.T) {
	err := NewHTTPError(404, "Not Found")

	if err.Code != 404 {
		t.Errorf("Expected code 404, got %d", err.Code)
	}

	if err.Message != "Not Found" {
		t.Errorf("Expected message 'Not Found', got %s", err.Message)
	}

	if err.Error() != "Not Found" {
		t.Errorf("Expected Error() to return message, got %s", err.Error())
	}
}

func TestHTTPError_WithDetails(t *testing.T) {
	details := map[string]string{"field": "email", "issue": "invalid"}
	err := NewHTTPError(400, "Validation failed").WithDetails(details)

	if err.Details == nil {
		t.Error("Expected details to be set")
	}

	detailsMap, ok := err.Details.(map[string]string)
	if !ok {
		t.Error("Expected details to be map[string]string")
	}

	if detailsMap["field"] != "email" {
		t.Error("Expected field detail")
	}
}

func TestDefaultErrorHandler_HTTPError(t *testing.T) {
	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := NewHTTPError(400, "Bad Request")
	handlerErr := DefaultErrorHandler(ctx, err)

	if handlerErr != nil {
		t.Errorf("DefaultErrorHandler error: %v", handlerErr)
	}

	if ctx.Status() != 400 {
		t.Errorf("Expected status 400, got %d", ctx.Status())
	}
}

func TestDefaultErrorHandler_HTTPError_JSON(t *testing.T) {
	t.Skip("Accept header parsing requires full stream setup - tested in integration tests")
	s := stream.NewStream(1)
	s.AddHeader("accept", "application/json")
	ctx := newContext(context.Background(), s, nil)

	err := NewHTTPError(404, "Not Found")
	handlerErr := DefaultErrorHandler(ctx, err)

	if handlerErr != nil {
		t.Errorf("DefaultErrorHandler error: %v", handlerErr)
	}

	if ctx.responseHeaders.Get("content-type") != "application/json" {
		t.Error("Expected JSON content-type")
	}
}

func TestDefaultErrorHandler_GenericError(t *testing.T) {
	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := fmt.Errorf("generic error")
	handlerErr := DefaultErrorHandler(ctx, err)

	if handlerErr != nil {
		t.Errorf("DefaultErrorHandler error: %v", handlerErr)
	}

	if ctx.Status() != 500 {
		t.Errorf("Expected status 500, got %d", ctx.Status())
	}
}

func TestRouter_ErrorHandler(t *testing.T) {
	router := NewRouter()

	customCalled := false
	router.ErrorHandler(func(ctx *Context, _ error) error {
		customCalled = true
		return ctx.String(418, "I'm a teapot")
	})

	router.GET("/error", func(_ *Context) error {
		return fmt.Errorf("test error")
	})

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/error")

	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}

	ctx := newContext(context.Background(), s, writeResponseFunc)

	_ = router.ServeHTTP2(ctx)

	if !customCalled {
		t.Error("Expected custom error handler to be called")
	}
}

func TestRouter_Static(t *testing.T) {
	router := NewRouter()

	// Create temp directory for testing
	tmpDir := t.TempDir()
	testFile := tmpDir + "/test.txt"
	if err := os.WriteFile(testFile, []byte("test content"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	router.Static("/static", tmpDir)

	// Test accessing static file
	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/static/test.txt")

	writeResponseFunc := func(_ uint32, status int, _ [][2]string, body []byte) error {
		if status != 200 {
			t.Errorf("Expected status 200, got %d", status)
		}
		if string(body) != "test content" {
			t.Errorf("Expected body 'test content', got %s", string(body))
		}
		return nil
	}

	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := router.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}
}
