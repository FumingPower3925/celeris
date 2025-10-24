package celeris

import (
	"context"
	"testing"

	"github.com/albertbausili/celeris/internal/h2/stream"
)

func TestNew(t *testing.T) {
	config := DefaultConfig()
	server := New(config)

	if server == nil {
		t.Fatal("Expected non-nil server")
	}

	if server.config.Addr != config.Addr {
		t.Errorf("Expected addr %s, got %s", config.Addr, server.config.Addr)
	}
}

func TestNewWithDefaults(t *testing.T) {
	server := NewWithDefaults()

	if server == nil {
		t.Fatal("Expected non-nil server")
	}

	if server.config.Addr != ":8080" {
		t.Errorf("Expected default addr :8080, got %s", server.config.Addr)
	}
}

func TestServer_Handler(t *testing.T) {
	server := NewWithDefaults()
	handler := HandlerFunc(func(ctx *Context) error {
		return ctx.String(200, "ok")
	})

	result := server.Handler(handler)

	if result != server {
		t.Error("Expected Handler to return server for chaining")
	}

	if server.handler == nil {
		t.Error("Expected handler to be set")
	}
}

func TestServer_Stop(t *testing.T) {
	server := NewWithDefaults()

	// Calling stop on server that hasn't started should not error
	err := server.Stop(context.Background())
	if err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}

func TestStreamHandlerAdapter_SetProcessor(t *testing.T) {
	adapter := &streamHandlerAdapter{}

	processor := &stream.Processor{}
	adapter.SetProcessor(processor)

	if adapter.processor != processor {
		t.Error("Expected processor to be set")
	}
}

func TestStreamHandlerAdapter_SetConnection(t *testing.T) {
	adapter := &streamHandlerAdapter{}

	// Mock ResponseWriter
	var conn stream.ResponseWriter

	adapter.SetConnection(conn)

	if adapter.currentConn != conn {
		t.Error("Expected connection to be set")
	}
}

func TestStreamHandlerAdapter_HandleStream(t *testing.T) {
	// Skip this test as it requires proper stream/response writer integration
	// This functionality is tested in integration tests
	t.Skip("StreamHandlerAdapter test requires full stream setup - tested in integration tests")
}
