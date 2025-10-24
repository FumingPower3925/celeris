package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// TestMiddlewareExecution tests that middleware executes correctly
func TestMiddlewareExecution(t *testing.T) {
	var middlewareCalled bool
	var mu sync.Mutex

	router := celeris.NewRouter()

	router.Use(func(next celeris.Handler) celeris.Handler {
		return celeris.HandlerFunc(func(ctx *celeris.Context) error {
			mu.Lock()
			middlewareCalled = true
			mu.Unlock()
			return next.ServeHTTP2(ctx)
		})
	})

	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.String(200, "ok")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 5*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/test", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	called := middlewareCalled
	mu.Unlock()

	if !called {
		t.Error("Middleware was not called")
	}
}

// TestLoggerMiddleware tests logger middleware
func TestLoggerMiddleware(t *testing.T) {
	router := celeris.NewRouter()
	router.Use(celeris.Logger())

	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.String(200, "ok")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 5*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/test", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

// TestRecoveryMiddleware tests panic recovery
func TestRecoveryMiddleware(t *testing.T) {
	router := celeris.NewRouter()
	router.Use(celeris.Recovery())

	router.GET("/panic", func(ctx *celeris.Context) error {
		panic("test panic")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 5*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/panic", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Server should recover and return 500
	if resp.StatusCode != 500 {
		t.Errorf("Expected status 500 after panic, got %d", resp.StatusCode)
	}
}
