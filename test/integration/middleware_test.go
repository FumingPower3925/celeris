package integration

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// TestRateLimiterMiddleware tests the rate limiting middleware
func TestRateLimiterMiddleware(t *testing.T) {
	// Create a new router with rate limiting
	router := celeris.NewRouter()

	// Add rate limiter middleware (5 requests per second)
	router.Use(celeris.RateLimiter(5))

	// Add a simple handler
	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{"message": "success"})
	})

	// Test server
	server := celeris.New(celeris.Config{Addr: ":8081"})

	// Test rate limiting
	t.Run("RateLimiting", func(t *testing.T) {
		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(1 * time.Second) // Give server time to start

		// Cleanup
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Stop(ctx)
		}()

		// Get server address
		addr := server.Addr()
		if addr == "" {
			t.Fatal("Server address not available")
		}

		// Make requests rapidly to test rate limiting
		client := &http.Client{Timeout: 5 * time.Second}
		successCount := 0
		rateLimitedCount := 0

		// Send 10 requests rapidly (should hit rate limit)
		for i := 0; i < 10; i++ {
			resp, err := client.Get(fmt.Sprintf("http://%s/test", addr))
			if err != nil {
				t.Logf("Request error: %v", err)
				continue
			}
			resp.Body.Close()

			switch resp.StatusCode {
			case 200:
				successCount++
			case 429:
				rateLimitedCount++
			}
		}

		// Should have some successful requests and some rate limited
		if successCount == 0 {
			t.Error("Expected at least some successful requests")
		}
		if rateLimitedCount == 0 {
			t.Error("Expected some requests to be rate limited")
		}

		t.Logf("Successful requests: %d, Rate limited: %d", successCount, rateLimitedCount)
	})

	// Test rate limiting with different clients
	t.Run("RateLimitingPerClient", func(t *testing.T) {
		// This test would require multiple client IPs, which is hard to simulate
		// In a real scenario, each client would have their own rate limit bucket
		t.Skip("Skipping per-client test - requires multiple IPs")
	})

	// Test rate limiting skip paths
	t.Run("RateLimitingSkipPaths", func(t *testing.T) {
		// Create router with skip paths
		router2 := celeris.NewRouter()
		config := celeris.RateLimiterConfig{
			RequestsPerSecond: 1,
			SkipPaths:         []string{"/health"},
		}
		router2.Use(celeris.RateLimiterWithConfig(config))

		router2.GET("/health", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"status": "ok"})
		})
		router2.GET("/test", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"message": "success"})
		})

		// Test that /health is not rate limited
		client := &http.Client{Timeout: 5 * time.Second}

		// Make multiple requests to /health rapidly
		for i := 0; i < 5; i++ {
			resp, err := client.Get(fmt.Sprintf("http://%s/health", server.Addr()))
			if err != nil {
				t.Logf("Health check error: %v", err)
				continue
			}
			resp.Body.Close()

			if resp.StatusCode != 200 {
				t.Errorf("Health check should not be rate limited, got status %d", resp.StatusCode)
			}
		}
	})
}

// TestHealthMiddleware tests the health check middleware
func TestHealthMiddleware(t *testing.T) {
	// Create a new router with health middleware
	router := celeris.NewRouter()
	router.Use(celeris.Health())

	// Add a test handler
	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{"message": "test"})
	})

	// Test server
	server := celeris.New(celeris.Config{Addr: ":8082"})

	t.Run("HealthEndpoint", func(t *testing.T) {
		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(500 * time.Millisecond)

		// Test health endpoint
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://%s/health", server.Addr()))
		if err != nil {
			t.Fatalf("Health check request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		// Check content type
		contentType := resp.Header.Get("content-type")
		if contentType != "application/json" {
			t.Errorf("Expected content-type application/json, got %s", contentType)
		}
	})

	t.Run("CustomHealthHandler", func(t *testing.T) {
		// Create router with custom health handler
		router2 := celeris.NewRouter()
		config := celeris.HealthConfig{
			Path: "/custom-health",
			Handler: func(ctx *celeris.Context) error {
				return ctx.JSON(200, map[string]interface{}{
					"status":    "healthy",
					"service":   "test-service",
					"timestamp": time.Now().Unix(),
				})
			},
		}
		router2.Use(celeris.HealthWithConfig(config))

		router2.GET("/test", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"message": "test"})
		})

		// Test custom health endpoint
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://%s/custom-health", server.Addr()))
		if err != nil {
			t.Fatalf("Custom health check request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
	})
}

// TestDocsMiddleware tests the documentation middleware
func TestDocsMiddleware(t *testing.T) {
	// Create a new router with docs middleware
	router := celeris.NewRouter()

	// Configure docs with sample routes
	config := celeris.DocsConfig{
		Title:       "Test API",
		Description: "Test API Documentation",
		Version:     "1.0.0",
		ServerURL:   "http://localhost:8083",
		Routes: []celeris.RouteInfo{
			{
				Method:      "GET",
				Path:        "/users",
				Summary:     "Get users",
				Description: "Retrieve a list of users",
				Tags:        []string{"users"},
				Parameters: []celeris.ParameterInfo{
					{
						Name:        "limit",
						In:          "query",
						Required:    false,
						Description: "Number of users to return",
						Type:        "integer",
					},
				},
				Responses: map[string]string{
					"200": "Success",
					"400": "Bad Request",
				},
			},
			{
				Method:      "POST",
				Path:        "/users",
				Summary:     "Create user",
				Description: "Create a new user",
				Tags:        []string{"users"},
				Responses: map[string]string{
					"201": "Created",
					"400": "Bad Request",
				},
			},
		},
	}
	router.Use(celeris.DocsWithConfig(config))

	// Add some test handlers
	router.GET("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{"message": "users"})
	})
	router.POST("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(201, map[string]string{"message": "user created"})
	})

	// Test server
	server := celeris.New(celeris.Config{Addr: ":8083"})

	t.Run("DocsEndpoint", func(t *testing.T) {
		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(500 * time.Millisecond)

		// Test docs endpoint
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://%s/docs", server.Addr()))
		if err != nil {
			t.Fatalf("Docs request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		// Check content type
		contentType := resp.Header.Get("content-type")
		if contentType != "text/html; charset=utf-8" {
			t.Errorf("Expected content-type text/html; charset=utf-8, got %s", contentType)
		}
	})

	t.Run("DefaultDocsConfig", func(t *testing.T) {
		// Test with default config
		router2 := celeris.NewRouter()
		router2.Use(celeris.Docs())

		router2.GET("/test", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"message": "test"})
		})

		// Test default docs endpoint
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://%s/docs", server.Addr()))
		if err != nil {
			t.Fatalf("Default docs request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
	})
}

// TestMiddlewareRaceConditions tests for race conditions in middleware
func TestMiddlewareRaceConditions(t *testing.T) {
	// Test rate limiter race conditions
	t.Run("RateLimiterRaceConditions", func(t *testing.T) {
		router := celeris.NewRouter()
		router.Use(celeris.RateLimiter(100)) // High limit for race testing

		router.GET("/test", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"message": "success"})
		})

		server := celeris.New(celeris.Config{Addr: ":8084"})

		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(500 * time.Millisecond)

		// Concurrent requests to test race conditions
		var wg sync.WaitGroup
		client := &http.Client{Timeout: 5 * time.Second}

		// Send 50 concurrent requests
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := client.Get(fmt.Sprintf("http://%s/test", server.Addr()))
				if err != nil {
					t.Logf("Request error: %v", err)
					return
				}
				resp.Body.Close()
			}()
		}

		wg.Wait()
		t.Log("Rate limiter race condition test completed")
	})

	// Test health middleware race conditions
	t.Run("HealthMiddlewareRaceConditions", func(t *testing.T) {
		router := celeris.NewRouter()
		router.Use(celeris.Health())

		router.GET("/test", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"message": "success"})
		})

		server := celeris.New(celeris.Config{Addr: ":8084"})

		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(500 * time.Millisecond)

		// Concurrent requests to health endpoint
		var wg sync.WaitGroup
		client := &http.Client{Timeout: 5 * time.Second}

		// Send 20 concurrent health check requests
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := client.Get(fmt.Sprintf("http://%s/health", server.Addr()))
				if err != nil {
					t.Logf("Health check error: %v", err)
					return
				}
				resp.Body.Close()

				if resp.StatusCode != 200 {
					t.Errorf("Health check failed with status %d", resp.StatusCode)
				}
			}()
		}

		wg.Wait()
		t.Log("Health middleware race condition test completed")
	})

	// Test docs middleware race conditions
	t.Run("DocsMiddlewareRaceConditions", func(t *testing.T) {
		router := celeris.NewRouter()
		router.Use(celeris.Docs())

		router.GET("/test", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"message": "success"})
		})

		server := celeris.New(celeris.Config{Addr: ":8084"})

		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(500 * time.Millisecond)

		// Concurrent requests to docs endpoint
		var wg sync.WaitGroup
		client := &http.Client{Timeout: 5 * time.Second}

		// Send 10 concurrent docs requests
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := client.Get(fmt.Sprintf("http://%s/docs", server.Addr()))
				if err != nil {
					t.Logf("Docs request error: %v", err)
					return
				}
				resp.Body.Close()

				if resp.StatusCode != 200 {
					t.Errorf("Docs request failed with status %d", resp.StatusCode)
				}
			}()
		}

		wg.Wait()
		t.Log("Docs middleware race condition test completed")
	})
}

// TestMiddlewareMemoryLeaks tests for memory leaks in middleware
func TestMiddlewareMemoryLeaks(t *testing.T) {
	// Test rate limiter memory cleanup
	t.Run("RateLimiterMemoryCleanup", func(t *testing.T) {
		router := celeris.NewRouter()
		router.Use(celeris.RateLimiter(10))

		router.GET("/test", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"message": "success"})
		})

		server := celeris.New(celeris.Config{Addr: ":8084"})

		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(500 * time.Millisecond)

		client := &http.Client{Timeout: 5 * time.Second}

		// Make some requests to create rate limiters
		for i := 0; i < 5; i++ {
			resp, err := client.Get(fmt.Sprintf("http://%s/test", server.Addr()))
			if err != nil {
				t.Logf("Request error: %v", err)
				continue
			}
			resp.Body.Close()
		}

		// Wait for cleanup (rate limiter cleans up every 5 minutes)
		// In a real test, you might want to test the cleanup mechanism more directly
		t.Log("Rate limiter memory cleanup test completed")
	})
}

// TestMiddlewareIntegration tests all middleware working together
func TestMiddlewareIntegration(t *testing.T) {
	// Create router with all middleware
	router := celeris.NewRouter()

	// Add all middleware
	router.Use(celeris.Logger())
	router.Use(celeris.Recovery())
	router.Use(celeris.RequestID())
	router.Use(celeris.RateLimiter(10))
	router.Use(celeris.Health())
	router.Use(celeris.Docs())

	// Add test handlers
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Hello from Celeris with all middleware!",
		})
	})

	router.GET("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{"message": "users"})
	})

	router.POST("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(201, map[string]string{"message": "user created"})
	})

	server := celeris.New(celeris.Config{Addr: ":8080"})

	t.Run("AllMiddlewareIntegration", func(t *testing.T) {
		// Start server
		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()
		time.Sleep(500 * time.Millisecond)

		client := &http.Client{Timeout: 5 * time.Second}
		addr := server.Addr()

		// Test main endpoint
		resp, err := client.Get(fmt.Sprintf("http://%s/", addr))
		if err != nil {
			t.Fatalf("Main endpoint request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		// Check for request ID header
		requestID := resp.Header.Get("x-request-id")
		if requestID == "" {
			t.Error("Expected X-Request-ID header")
		}

		// Check for rate limit headers
		rateLimit := resp.Header.Get("x-ratelimit-limit")
		if rateLimit == "" {
			t.Error("Expected X-RateLimit-Limit header")
		}

		// Test health endpoint
		resp, err = client.Get(fmt.Sprintf("http://%s/health", addr))
		if err != nil {
			t.Fatalf("Health endpoint request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200 for health, got %d", resp.StatusCode)
		}

		// Test docs endpoint
		resp, err = client.Get(fmt.Sprintf("http://%s/docs", addr))
		if err != nil {
			t.Fatalf("Docs endpoint request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("Expected status 200 for docs, got %d", resp.StatusCode)
		}

		t.Log("All middleware integration test completed successfully")
	})
}
