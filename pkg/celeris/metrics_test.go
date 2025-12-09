package celeris

import (
	"context"
	"testing"

	"github.com/FumingPower3925/celeris/internal/h2/stream"
)

func TestPrometheus_Middleware(t *testing.T) {
	prometheus := Prometheus()

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := prometheus(handler)

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

	// Metrics are collected in background, just verify no errors
}

func TestPrometheusWithConfig_SkipPaths(t *testing.T) {
	config := PrometheusConfig{
		Subsystem: "test",
		SkipPaths: []string{"/metrics"},
	}
	prometheus := PrometheusWithConfig(config)

	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	wrapped := prometheus(handler)

	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")
	s.AddHeader(":path", "/metrics")

	// Add mock write response function
	writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
		return nil
	}

	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	// Should not collect metrics for skipped path
}

func TestPrometheusConfig_Defaults(t *testing.T) {
	config := DefaultPrometheusConfig()

	if config.Subsystem != "http" {
		t.Errorf("Expected subsystem 'http', got %s", config.Subsystem)
	}

	if len(config.SkipPaths) == 0 {
		t.Error("Expected default skip paths")
	}

	if len(config.Buckets) == 0 {
		t.Error("Expected default buckets")
	}
}

func TestPrometheusMetrics_StatusCodes(t *testing.T) {
	prometheus := Prometheus()

	tests := []struct {
		name   string
		status int
	}{
		{"success", 200},
		{"created", 201},
		{"bad request", 400},
		{"not found", 404},
		{"internal error", 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := HandlerFunc(func(ctx *Context) error {
				return ctx.String(tt.status, "test")
			})

			wrapped := prometheus(handler)

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

			if ctx.Status() != tt.status {
				t.Errorf("Expected status %d, got %d", tt.status, ctx.Status())
			}
		})
	}
}
