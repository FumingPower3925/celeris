//go:build ignore

package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	time.Sleep(1 * time.Second)
	fmt.Println("Testing Streaming & SSE...")

	// Test SSE
	fmt.Println("Checking /events (SSE)...")
	resp, err := http.Get("http://localhost:8080/events")
	if err != nil {
		fmt.Printf("FAIL: /events - %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		fmt.Printf("FAIL: Expected Content-Type text/event-stream, got %s\n", ct)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(resp.Body)
	eventCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("Received: %s\n", line)
		if strings.HasPrefix(line, "data:") {
			eventCount++
			if eventCount >= 3 {
				break
			}
		}
	}
	if eventCount < 3 {
		fmt.Printf("FAIL: Expected at least 3 events, got %d\n", eventCount)
		os.Exit(1)
	}
	fmt.Println("PASS: /events")

	// Test Stream
	fmt.Println("Checking /stream...")
	respStream, err := http.Get("http://localhost:8080/stream")
	if err != nil {
		fmt.Printf("FAIL: /stream - %v\n", err)
		os.Exit(1)
	}
	defer respStream.Body.Close()

	// Just read a few chunks
	buf := make([]byte, 1024)
	n, err := respStream.Body.Read(buf)
	if err != nil && n == 0 {
		fmt.Printf("FAIL: Read stream failed - %v\n", err)
		os.Exit(1)
	}
	if n > 0 {
		fmt.Printf("PASS: /stream (Received %d bytes)\n", n)
	} else {
		fmt.Println("FAIL: /stream (No data)")
		os.Exit(1)
	}

	fmt.Println("SUCCESS: Streaming Verified")
}
