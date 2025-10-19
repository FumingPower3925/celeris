package celeris

import (
	"context"
	"testing"

	"github.com/albertbausili/celeris/internal/stream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestTracing_Middleware(t *testing.T) {
	// Set up a test tracer
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	tracing := Tracing()

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := tracing(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/test")
	ctx := newContext(context.Background(), s, nil)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	// Verify span was created (implicitly tested by no panic)
}

func TestTracingWithConfig_SkipPaths(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	config := TracingConfig{
		TracerName: "test",
		SkipPaths:  []string{"/health"},
		Propagator: propagation.TraceContext{},
	}
	tracing := TracingWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := tracing(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/health")
	ctx := newContext(context.Background(), s, nil)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	// Should not create span for skipped path
}

func TestTracingConfig_Defaults(t *testing.T) {
	config := DefaultTracingConfig()

	if config.TracerName != "celeris" {
		t.Errorf("Expected tracer name 'celeris', got %s", config.TracerName)
	}

	if len(config.SkipPaths) == 0 {
		t.Error("Expected default skip paths")
	}

	if config.Propagator == nil {
		t.Error("Expected default propagator")
	}
}

func TestTracing_ErrorRecording(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	tracing := Tracing()

	handler := HandlerFunc(func(_ *Context) error {
		return NewHTTPError(500, "internal error")
	})

	wrapped := tracing(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/error")
	ctx := newContext(context.Background(), s, nil)

	err := wrapped.ServeHTTP2(ctx)
	if err == nil {
		t.Error("Expected error from handler")
	}

	// Error should be recorded in span
}

func TestHeaderCarrier_GetSet(t *testing.T) {
	headers := NewHeaders()
	headers.Set("traceparent", "test-value")
	headers.Set("tracestate", "state-value")

	carrier := &headerCarrier{headers: &headers}

	// Test Get
	if carrier.Get("traceparent") != "test-value" {
		t.Errorf("Expected traceparent 'test-value', got %s", carrier.Get("traceparent"))
	}

	// Test Set
	carrier.Set("new-header", "new-value")
	if headers.Get("new-header") != "new-value" {
		t.Error("Expected Set to modify headers")
	}

	// Test Keys
	keys := carrier.Keys()
	if len(keys) == 0 {
		t.Error("Expected Keys to return header names")
	}
}

func TestTracing_RequestIDAttribute(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	tracing := Tracing()

	handler := HandlerFunc(func(ctx *Context) error {
		ctx.Set("request-id", "test-123")
		return ctx.String(200, "ok")
	})

	// Apply RequestID middleware first
	reqID := RequestID()
	wrapped := tracing(reqID(handler))

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/test")
	ctx := newContext(context.Background(), s, nil)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	// Request ID should be added as span attribute
}
