package integration

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

func waitForServerFeature(url string) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// TestCompressionMiddleware verifies that the compression middleware correctly handles
// responses, applies the correct headers, and compresses the body.
func TestCompressionMiddleware(t *testing.T) {
	t.Run("GzipCompression", func(t *testing.T) {
		router := celeris.NewRouter()

		// Use default compression (gzip enabled by default)
		router.Use(celeris.Compress())

		largeData := strings.Repeat("A", 2048) // > 1024 bytes default min size
		router.GET("/large", func(ctx *celeris.Context) error {
			return ctx.String(200, "%s", largeData)
		})

		server := celeris.New(celeris.Config{
			Addr:     ":8090", // Unique port
			EnableH1: true,
		})

		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()

		if !waitForServerFeature("http://localhost:8090/large") {
			t.Fatal("Server failed to start")
		}

		defer func() {
			server.Stop(context.Background())
		}()

		// Request with gzip support
		req, _ := http.NewRequest("GET", "http://localhost:8090/large", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Verify Headers
		if resp.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("Expected Content-Encoding: gzip, got %s", resp.Header.Get("Content-Encoding"))
		}

		// Verify Body is valid gzip
		reader, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("Failed to create gzip reader (body might not be compressed): %v", err)
		}
		defer reader.Close()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("Failed to read compressed body: %v", err)
		}

		if string(decompressed) != largeData {
			t.Errorf("Decompressed data mismatch. Got length %d, expected %d", len(decompressed), len(largeData))
		}
	})
}

// TestSSEVerification verifies that the SSE implementation correctly streams events
// with the proper headers, chunked transfer encoding, and data format.
func TestSSEVerification(t *testing.T) {
	t.Run("SSEStream", func(t *testing.T) {
		router := celeris.NewRouter()

		router.GET("/health-check", func(ctx *celeris.Context) error {
			return ctx.String(200, "OK")
		})

		router.GET("/events", func(ctx *celeris.Context) error {
			// Send 3 events
			for i := 1; i <= 3; i++ {
				err := ctx.SSE(celeris.SSEEvent{
					ID:    fmt.Sprintf("%d", i),
					Event: "message",
					Data:  fmt.Sprintf("event-%d", i),
				})
				if err != nil {
					return err
				}
				// Simulate delay slightly to ensure flushing works (no sleep here for test speed, but conceptual)
			}
			return nil
		})

		server := celeris.New(celeris.Config{
			Addr:     ":8091", // Unique port
			EnableH1: true,
		})

		go func() {
			if err := server.ListenAndServe(router); err != nil {
				t.Logf("Server error: %v", err)
			}
		}()

		if !waitForServerFeature("http://localhost:8091/health-check") {
			t.Fatal("Server failed to start")
		}

		defer func() {
			server.Stop(context.Background())
		}()

		req, _ := http.NewRequest("GET", "http://localhost:8091/events", nil)
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Verify Headers
		if resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Errorf("Expected Content-Type: text/event-stream, got %s", resp.Header.Get("Content-Type"))
		}

		// Check Transfer-Encoding (Go http.Client strips the header and puts it in TransferEncoding slice)
		isChunked := false
		for _, te := range resp.TransferEncoding {
			if te == "chunked" {
				isChunked = true
				break
			}
		}
		// Fallback check if it wasn't stripped
		if !isChunked && resp.Header.Get("Transfer-Encoding") == "chunked" {
			isChunked = true
		}

		if !isChunked {
			t.Errorf("Expected Transfer-Encoding: chunked, got %v", resp.TransferEncoding)
		}

		// Read entire body for debugging if scanning fails
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read body: %v", err)
		}

		// Parse events from the read body
		eventCount := 0
		lines := strings.Split(string(body), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "data: event-") {
				eventCount++
			}
		}

		if eventCount != 3 {
			t.Errorf("Expected 3 data events, got %d. Body:\n%s", eventCount, string(body))
		}
	})
}
