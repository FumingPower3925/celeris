package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// TestCompression verifies the compress middleware works when Accept-Encoding is present
// Note: Go's http.Client controls compression headers, so we test the middleware directly in unit tests
func TestCompression(t *testing.T) {
	// Skip this test as it's better tested in middleware_test.go where we have full control
	t.Skip("Compression is tested in detail in pkg/celeris/middleware_test.go")
}

// TestQueryParameters verifies query parameter parsing
func TestQueryParameters(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/search", func(ctx *celeris.Context) error {
		q := ctx.Query("q")
		page, _ := ctx.QueryInt("page")
		enabled := ctx.QueryBool("enabled")

		return ctx.JSON(200, map[string]interface{}{
			"query":   q,
			"page":    page,
			"enabled": enabled,
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	defer server.Stop(context.Background())
	if err := waitForServer(config.Addr, 3*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}

	client := createHTTP2Client()
	resp, err := client.Get("http://localhost" + config.Addr + "/search?q=golang&page=2&enabled=true")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "golang") {
		t.Error("Expected query 'golang' in response")
	}
	if !strings.Contains(bodyStr, "\"page\":2") {
		t.Error("Expected page 2 in response")
	}
}

// TestCookies verifies cookie handling
func TestCookies(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/set-cookie", func(ctx *celeris.Context) error {
		ctx.SetCookie(&http.Cookie{
			Name:  "session",
			Value: "test123",
			Path:  "/",
		})
		return ctx.String(200, "Cookie set")
	})

	router.GET("/get-cookie", func(ctx *celeris.Context) error {
		session := ctx.Cookie("session")
		return ctx.String(200, "Session: %s", session)
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	defer server.Stop(context.Background())
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}

	client := createHTTP2Client()

	// Set cookie
	resp, err := client.Get("http://localhost" + config.Addr + "/set-cookie")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Check Set-Cookie header
	setCookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(setCookie, "session=test123") {
		t.Errorf("Expected Set-Cookie with session=test123, got %s", setCookie)
	}
}

// TestStaticFiles verifies static file serving
func TestStaticFiles(t *testing.T) {
	// Create temporary directory and file
	tmpDir := t.TempDir()
	testFile := tmpDir + "/test.txt"
	testContent := "Hello from static file"
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	router := celeris.NewRouter()
	router.Static("/static", tmpDir)

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	defer server.Stop(context.Background())
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}

	client := createHTTP2Client()
	resp, err := client.Get("http://localhost" + config.Addr + "/static/test.txt")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != testContent {
		t.Errorf("Expected body %s, got %s", testContent, string(body))
	}

	// Check ETag header
	if resp.Header.Get("Etag") == "" {
		t.Error("Expected ETag header")
	}

	// Test 304 Not Modified
	etag := resp.Header.Get("Etag")
	req, _ := http.NewRequest("GET", "http://localhost"+config.Addr+"/static/test.txt", nil)
	req.Header.Set("If-None-Match", etag)

	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 304 {
		t.Errorf("Expected status 304, got %d", resp2.StatusCode)
	}
}

// TestErrorHandling verifies error handler functionality
func TestErrorHandling(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/error", func(ctx *celeris.Context) error {
		return celeris.NewHTTPError(400, "Bad Request").WithDetails(map[string]string{
			"field": "username",
			"issue": "required",
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	defer server.Stop(context.Background())
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}

	client := createHTTP2Client()
	req, _ := http.NewRequest("GET", "http://localhost"+config.Addr+"/error", nil)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "Bad Request") {
		t.Error("Expected error message in response")
	}
	if !strings.Contains(bodyStr, "username") {
		t.Error("Expected details in response")
	}
}

// TestStreaming verifies streaming responses
func TestStreaming(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/stream", func(ctx *celeris.Context) error {
		ctx.SetHeader("content-type", "text/plain")
		ctx.SetStatus(200)

		for i := 0; i < 3; i++ {
			fmt.Fprintf(ctx.Writer(), "Chunk %d\n", i)
			if err := ctx.Flush(); err != nil {
				return err
			}
		}
		return nil
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	defer server.Stop(context.Background())
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}

	client := createHTTP2Client()
	resp, err := client.Get("http://localhost" + config.Addr + "/stream")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "Chunk 0") {
		t.Error("Expected Chunk 0 in response")
	}
	if !strings.Contains(bodyStr, "Chunk 1") {
		t.Error("Expected Chunk 1 in response")
	}
	if !strings.Contains(bodyStr, "Chunk 2") {
		t.Error("Expected Chunk 2 in response")
	}
}

// TestSSE verifies Server-Sent Events
func TestSSE(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/events", func(ctx *celeris.Context) error {
		for i := 0; i < 3; i++ {
			if err := ctx.SSE(celeris.SSEEvent{
				ID:    fmt.Sprintf("%d", i),
				Event: "message",
				Data:  fmt.Sprintf("Event %d", i),
			}); err != nil {
				return err
			}
			if err := ctx.Flush(); err != nil {
				return err
			}
		}
		return nil
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	defer server.Stop(context.Background())
	if err := waitForServer(config.Addr, 3*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}

	client := createHTTP2Client()
	resp, err := client.Get("http://localhost" + config.Addr + "/events")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Expected Content-Type text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "id: 0") {
		t.Error("Expected SSE id field")
	}
	if !strings.Contains(bodyStr, "event: message") {
		t.Error("Expected SSE event field")
	}
	if !strings.Contains(bodyStr, "data: Event 0") {
		t.Error("Expected SSE data field")
	}
}

// TestLoggerMiddlewareIntegration verifies logger integration
func TestLoggerMiddlewareIntegration(t *testing.T) {
	var buf bytes.Buffer
	logConfig := celeris.LoggerConfig{
		Output: &buf,
		Format: "text",
	}

	router := celeris.NewRouter()
	router.Use(celeris.LoggerWithConfig(logConfig))

	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.String(200, "ok")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() {
		if err := server.ListenAndServe(router); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()
	defer server.Stop(context.Background())

	time.Sleep(100 * time.Millisecond)

	client := createHTTP2Client()
	_, _ = client.Get("http://" + config.Addr + "/test")

	time.Sleep(100 * time.Millisecond)

	logOutput := buf.String()
	if !strings.Contains(logOutput, "GET") {
		t.Error("Expected GET in log output")
	}
	if !strings.Contains(logOutput, "/test") {
		t.Error("Expected /test in log output")
	}
	if !strings.Contains(logOutput, "200") {
		t.Error("Expected status 200 in log output")
	}
}

// Helper function to create HTTP/2 client - defined in other test files
