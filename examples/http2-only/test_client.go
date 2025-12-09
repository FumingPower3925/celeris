//go:build ignore

package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/http2"
)

func main() {
	// Wait for server to start
	time.Sleep(1 * time.Second)

	fmt.Println("Testing HTTP/2 Only Server (H2C)...")

	// Use H2C client
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true, // Allow H2C
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://localhost:8080/hello")
	if err != nil {
		fmt.Printf("FAIL: Request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.ProtoMajor != 2 {
		fmt.Printf("FAIL: Expected HTTP/2.0, got %d.%d\n", resp.ProtoMajor, resp.ProtoMinor)
		os.Exit(1)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Hello from HTTP/2 only!" {
		fmt.Printf("FAIL: Unexpected body: %s\n", string(body))
		os.Exit(1)
	}

	fmt.Printf("SUCCESS: Verified protocol %s and response.\n", resp.Proto)
}
