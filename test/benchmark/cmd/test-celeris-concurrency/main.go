// Package main provides a high-concurrency validation test for Celeris.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
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
	cfg.NumEventLoop = 10
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.MaxConcurrentStreams = 250

	srv := celeris.New(cfg)
	go func() { _ = srv.ListenAndServe(r) }()
	time.Sleep(500 * time.Millisecond)

	fmt.Println("Testing Celeris with HIGH CONCURRENCY...")
	fmt.Println("Running 100 concurrent clients, each making 10 requests...")

	var successCount atomic.Int64
	var errorCount atomic.Int64
	var wg sync.WaitGroup

	startTime := time.Now()

	// 100 concurrent clients
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			client := &http.Client{
				Transport: &http2.Transport{
					AllowHTTP: true,
					DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
						return net.Dial(network, addr)
					},
				},
				Timeout: 5 * time.Second,
			}

			// Each client makes 10 requests
			for j := 0; j < 10; j++ {
				resp, err := client.Get("http://localhost:50000/bench")
				if err != nil {
					errorCount.Add(1)
					if clientID < 3 && j == 0 { // Only print first few errors
						fmt.Printf("❌ Client %d request %d FAILED: %v\n", clientID, j+1, err)
					}
					continue
				}
				_, _ = io.ReadAll(resp.Body)
				_ = resp.Body.Close()

				if resp.StatusCode == 200 && resp.Proto == "HTTP/2.0" {
					successCount.Add(1)
				} else {
					errorCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(startTime)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = srv.Stop(ctx)
	cancel()

	totalRequests := successCount.Load() + errorCount.Load()
	successRate := float64(successCount.Load()) / float64(totalRequests) * 100
	rps := float64(totalRequests) / elapsed.Seconds()

	fmt.Printf("\n=== RESULTS ===\n")
	fmt.Printf("Total requests: %d\n", totalRequests)
	fmt.Printf("Successful: %d\n", successCount.Load())
	fmt.Printf("Failed: %d\n", errorCount.Load())
	fmt.Printf("Success rate: %.1f%%\n", successRate)
	fmt.Printf("Duration: %.2fs\n", elapsed.Seconds())
	fmt.Printf("RPS: %.0f\n", rps)

	if successRate >= 99.0 {
		fmt.Println("\n✅ HIGH CONCURRENCY TEST PASSED!")
	} else {
		fmt.Println("\n❌ Test had issues")
	}
}
