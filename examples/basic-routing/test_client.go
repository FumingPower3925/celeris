//go:build ignore

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	time.Sleep(1 * time.Second)
	fmt.Println("Testing Basic Routing...")

	endpoints := []struct {
		url    string
		expect string
	}{
		{"http://localhost:8080/hello", "Hello, World!"},
		{"http://localhost:8080/echo", "Echo, World!"},
		{"http://localhost:8080/users/123", "User ID: 123"},
	}

	for _, ep := range endpoints {
		resp, err := http.Get(ep.url)
		if err != nil {
			fmt.Printf("FAIL: %s - %v\n", ep.url, err)
			os.Exit(1)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if string(body) != ep.expect {
			fmt.Printf("FAIL: %s - Expected '%s', got '%s'\n", ep.url, ep.expect, string(body))
			os.Exit(1)
		}
		fmt.Printf("PASS: %s\n", ep.url)
	}
	fmt.Println("SUCCESS: Basic Routing Verified")
}
