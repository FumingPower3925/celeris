//go:build ignore
// +build ignore

// Package main is a test client to demonstrate rate limiting.
// This is a separate executable - run it with: go run test_client.go
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	results := map[int]int{} // status code -> count

	fmt.Println("Rate Limiting Test Client")
	fmt.Println("=========================")
	fmt.Println("Making 20 rapid sequential requests to http://localhost:8080/")
	fmt.Println("Rate limit: 1 request per second, burst size: 2")
	fmt.Println("Expected: First ~2 requests should be 200, rest should be 429")
	fmt.Println()

	// Verify we're connecting to the rate-limiting server
	fmt.Println("Verifying connection to rate-limiting server...")
	testResp, err := client.Get("http://localhost:8080/")
	if err != nil {
		fmt.Printf("ERROR: Cannot connect to server at http://localhost:8080/\n")
		fmt.Printf("Make sure the rate-limiting server is running:\n")
		fmt.Printf("  cd examples/rate-limiting && go run main.go\n")
		os.Exit(1)
	}

	// Check if rate limit headers are present (indicates rate-limiting server)
	limitHeader := testResp.Header.Get("x-ratelimit-limit")
	testResp.Body.Close()

	if limitHeader == "" {
		fmt.Printf("⚠️  WARNING: Server at http://localhost:8080/ does not appear to be the rate-limiting server!\n")
		fmt.Printf("   No rate-limit headers found. You may be connected to a different server.\n")
		fmt.Printf("   Make sure only the rate-limiting example server is running on port 8080.\n")
		fmt.Printf("   Continuing anyway...\n")
		fmt.Println()
	} else {
		fmt.Printf("✓ Connected to rate-limiting server (rate limit: %s req/sec)\n", limitHeader)
		fmt.Println()
	}

	// Make requests sequentially but faster than 1 req/sec to trigger rate limiting
	for i := 0; i < 20; i++ {
		resp, err := client.Get("http://localhost:8080/")
		if err != nil {
			fmt.Printf("Request %2d: ERROR - %v\n", i, err)
			continue
		}

		// Read body to completion
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		results[resp.StatusCode]++
		statusText := "OK"
		if resp.StatusCode == 429 {
			statusText = "Too Many Requests (Rate Limited)"
		}

		// Print rate limit headers for debugging
		limit := resp.Header.Get("x-ratelimit-limit")
		remaining := resp.Header.Get("x-ratelimit-remaining")
		fmt.Printf("Request %2d: Status %d (%s) [Limit: %s, Remaining: %s]\n",
			i, resp.StatusCode, statusText, limit, remaining)

		// Small delay (200ms) - faster than 1 req/sec to exceed rate limit
		// but slow enough to allow rate limiter to process each request
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Println()
	fmt.Println("Summary:")
	fmt.Println("========")
	for status, count := range results {
		switch status {
		case 200:
			fmt.Printf("  %d OK responses (within rate limit)\n", count)
		case 429:
			fmt.Printf("  %d Rate Limited (429) responses (exceeded rate limit)\n", count)
		default:
			fmt.Printf("  %d responses with status %d\n", count, status)
		}
	}
	fmt.Println()
	fmt.Println("Rate limiting is working correctly if you see:")
	fmt.Println("  - Some 200 OK responses (allowed within rate limit/burst)")
	fmt.Println("  - Some 429 Too Many Requests responses (exceeded rate limit)")
}
