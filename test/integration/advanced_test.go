package integration

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// TestCustomHeaders tests setting and reading custom headers
func TestCustomHeaders(t *testing.T) {
	router := celeris.NewRouter()
	router.GET("/headers", func(ctx *celeris.Context) error {
		ctx.SetHeader("X-Custom-Header", "test-value")
		ctx.SetHeader("X-Server", "celeris")
		return ctx.String(200, "ok")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/headers", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// HTTP/2 returns headers in lowercase per RFC 7540
	if value := resp.Header.Get("x-custom-header"); value != "test-value" {
		t.Errorf("Expected x-custom-header: test-value, got: %s", value)
	}
}

// TestStatusCodes tests various HTTP status codes
func TestStatusCodes(t *testing.T) {
	router := celeris.NewRouter()

	statusCodes := []int{200, 201, 204, 400, 404, 500}

	for _, code := range statusCodes {
		code := code
		path := fmt.Sprintf("/status/%d", code)
		router.GET(path, func(ctx *celeris.Context) error {
			if code == 204 {
				return ctx.NoContent(code)
			}
			return ctx.String(code, "Status: %d", code)
		})
	}

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()

	for _, expectedCode := range statusCodes {
		t.Run(fmt.Sprintf("Status%d", expectedCode), func(t *testing.T) {
			resp, err := client.Get(fmt.Sprintf("http://localhost%s/status/%d", config.Addr, expectedCode))
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != expectedCode {
				t.Errorf("Expected status %d, got %d", expectedCode, resp.StatusCode)
			}
		})
	}
}

// TestLargePayload tests handling of large responses
func TestLargePayload(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/large", func(ctx *celeris.Context) error {
		data := make(map[string]string)
		for i := 0; i < 100; i++ {
			data[fmt.Sprintf("key%d", i)] = strings.Repeat(fmt.Sprintf("value%d", i), 10)
		}
		return ctx.JSON(200, data)
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/large", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read body: %v", err)
	}

	if len(body) < 1000 {
		t.Errorf("Expected large body (>1KB), got %d bytes", len(body))
	}
}

// TestNestedGroups tests nested route groups
func TestNestedGroups(t *testing.T) {
	router := celeris.NewRouter()

	api := router.Group("/api")
	v1 := api.Group("/v1")
	v1.GET("/users", func(ctx *celeris.Context) error {
		return ctx.String(200, "v1 users")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() {
		server.ListenAndServe(router)
	}()

	time.Sleep(100 * time.Millisecond)
	defer server.Stop(context.Background())

	client := createHTTP2Client()

	resp, err := client.Get(fmt.Sprintf("http://localhost%s/api/v1/users", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "v1 users" {
		t.Errorf("Expected 'v1 users', got %s", string(body))
	}
}

// TestGracefulShutdown tests server graceful shutdown
func TestGracefulShutdown(t *testing.T) {
	router := celeris.NewRouter()
	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.String(200, "ok")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() {
		server.ListenAndServe(router)
	}()

	time.Sleep(100 * time.Millisecond)

	client := createHTTP2Client()

	// Make a request before shutdown
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/test", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = server.Stop(ctx)
	if err != nil {
		t.Errorf("Server shutdown error: %v", err)
	}

	// Verify server is stopped
	time.Sleep(100 * time.Millisecond)
	_, err = client.Get(fmt.Sprintf("http://localhost%s/test", config.Addr))
	if err == nil {
		t.Error("Expected request to fail after shutdown")
	}
}
