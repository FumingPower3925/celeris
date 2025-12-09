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
	fmt.Println("Testing Compression...")

	endpoints := []struct {
		url         string
		expectGzip  bool
		minBodySize int
	}{
		{"http://localhost:8080/small", false, 0},
		{"http://localhost:8080/large", true, 2048},
		{"http://localhost:8080/html", true, 0},
		{"http://localhost:8080/json", true, 0},
	}

	client := &http.Client{}

	for _, ep := range endpoints {
		req, _ := http.NewRequest("GET", ep.url, nil)
		req.Header.Set("Accept-Encoding", "gzip")

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("FAIL: %s - %v\n", ep.url, err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		ce := resp.Header.Get("Content-Encoding")
		if ep.expectGzip && ce != "gzip" {
			fmt.Printf("FAIL: %s - Expected gzip, got '%s'\n", ep.url, ce)
			os.Exit(1)
		} else if !ep.expectGzip && ce == "gzip" {
			fmt.Printf("FAIL: %s - Unexpected gzip\n", ep.url)
			os.Exit(1)
		}

		// Read body (transparently decompressed by Go client usually, but we check raw size if needed?
		// Actually Go client decompresses automatically unless DisableCompression is set.
		// So we primarily check the header which Go strips if it decompresses?
		// Wait, if Go client handles gzip, it removes Content-Encoding header from the Response struct object typically!
		// Let's modify the request to explicitly ask for it but maybe not let Go handle it?
		// Actually, standard `http.Client` DOES decompress and remove the header.
		// To check for header presence correctly without decompressing, we need `DisableCompression: true` in transport.
	}

	// Real check with raw transport
	rawClient := &http.Client{
		Transport: &http.Transport{
			DisableCompression: true,
		},
	}

	for _, ep := range endpoints {
		req, _ := http.NewRequest("GET", ep.url, nil)
		req.Header.Set("Accept-Encoding", "gzip")

		resp, err := rawClient.Do(req)
		if err != nil {
			fmt.Printf("FAIL: %s - %v\n", ep.url, err)
			os.Exit(1)
		}

		ce := resp.Header.Get("Content-Encoding")
		if ep.expectGzip && ce != "gzip" {
			fmt.Printf("FAIL: %s - Expected Content-Encoding: gzip, got '%s'\n", ep.url, ce)
			os.Exit(1)
		} else if !ep.expectGzip && ce == "gzip" {
			fmt.Printf("FAIL: %s - Unexpected gzip\n", ep.url)
			os.Exit(1)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// If expect gzip, body should be compressed data
		if ep.expectGzip {
			if len(body) == 0 {
				fmt.Printf("FAIL: %s - Empty body\n", ep.url)
				os.Exit(1)
			}
			// Check gzip magic bytes 1f 8b
			if body[0] != 0x1f || body[1] != 0x8b {
				fmt.Printf("FAIL: %s - Invalid gzip magic bytes\n", ep.url)
				os.Exit(1)
			}
		}

		fmt.Printf("PASS: %s (Gzip: %v)\n", ep.url, ce)
	}

	fmt.Println("SUCCESS: Compression Verified")
}
