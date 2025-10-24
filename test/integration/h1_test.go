package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// TestHTTP1BasicRequest tests basic HTTP/1.1 request-response cycle
func TestHTTP1BasicRequest(t *testing.T) {
	router := celeris.NewRouter()
	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"status": "ok",
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	// Use standard HTTP/1.1 client
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("http://localhost%s/test", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	t.Logf("Response: %s", string(body))
}

// TestHTTP1RouteParameters tests route parameter extraction for HTTP/1.1
func TestHTTP1RouteParameters(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/users/:id", func(ctx *celeris.Context) error {
		id := celeris.Param(ctx, "id")
		return ctx.JSON(200, map[string]string{
			"user_id": id,
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("http://localhost%s/users/123", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("Response: %s", string(body))

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

// TestHTTP1NotFound tests 404 handling for HTTP/1.1
func TestHTTP1NotFound(t *testing.T) {
	router := celeris.NewRouter()
	router.GET("/exists", func(ctx *celeris.Context) error {
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

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("http://localhost%s/notfound", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("Expected status 404, got %d", resp.StatusCode)
	}
}

// TestHTTP1POST tests POST request handling
func TestHTTP1POST(t *testing.T) {
	router := celeris.NewRouter()
	router.POST("/echo", func(ctx *celeris.Context) error {
		var data map[string]interface{}
		if err := ctx.BindJSON(&data); err != nil {
			return ctx.JSON(400, map[string]string{
				"error": "Invalid JSON",
			})
		}
		return ctx.JSON(200, data)
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() {
		server.ListenAndServe(router)
	}()

	time.Sleep(100 * time.Millisecond)
	defer server.Stop(context.Background())

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(
		fmt.Sprintf("http://localhost%s/echo", config.Addr),
		"application/json",
		bytes.NewBufferString(`{"message":"hello"}`),
	)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	t.Logf("Response: %s", string(body))
}
