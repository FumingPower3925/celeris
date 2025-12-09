//go:build ignore

package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	// Wait for server to start
	time.Sleep(1 * time.Second)

	fmt.Println("Testing HTTP/1.1 Only Server...")

	// Verify HTTP/1.1
	// We use a custom transport to Inspect protocol version
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			ForceAttemptHTTP2: false, // Ensure we don't upgrade if not allowed
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://localhost:8080/hello")
	if err != nil {
		fmt.Printf("FAIL: Request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.ProtoMajor != 1 {
		fmt.Printf("FAIL: Expected HTTP/1.1, got %d.%d\n", resp.ProtoMajor, resp.ProtoMinor)
		os.Exit(1)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Hello from HTTP/1.1 only!" {
		fmt.Printf("FAIL: Unexpected body: %s\n", string(body))
		os.Exit(1)
	}

	fmt.Printf("SUCCESS: Verified protocol %s and response.\n", resp.Proto)
}
