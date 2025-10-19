// Package main provides a small validation program for Celeris fixes.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
	"golang.org/x/net/http2"
)

func main() {
	r := celeris.NewRouter()
	r.GET("/bench", func(ctx *celeris.Context) error {
		return ctx.String(200, "ok")
	})

	cfg := celeris.DefaultConfig()
	cfg.Addr = ":50000"
	cfg.Multicore = false
	cfg.NumEventLoop = 1
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.MaxConcurrentStreams = 100

	srv := celeris.New(cfg)
	go func() { _ = srv.ListenAndServe(r) }()
	time.Sleep(500 * time.Millisecond)

	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
		Timeout: 5 * time.Second,
	}

	fmt.Println("Testing Celeris HTTP/2 HPACK fix...")
	fmt.Println("Sending 10 requests on the same connection...")

	successCount := 0
	for i := 0; i < 10; i++ {
		resp, err := client.Get("http://localhost:50000/bench")
		if err != nil {
			fmt.Printf("❌ Request %d FAILED: %v\n", i+1, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		fmt.Printf("✓ Request %d: status=%d, body=%s, proto=%s\n", i+1, resp.StatusCode, string(body), resp.Proto)
		successCount++
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = srv.Stop(ctx)
	cancel()

	fmt.Printf("\nResult: %d/10 requests succeeded\n", successCount)
	if successCount == 10 {
		fmt.Println("✅ HPACK compression error FIXED!")
	} else {
		fmt.Println("❌ Still having issues")
	}
}
