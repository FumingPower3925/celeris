// Package main benchmarks synchronous hybrid HTTP.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/albertbausili/celeris/test/benchmark/internal/client"
	"github.com/albertbausili/celeris/test/benchmark/internal/server"
	"golang.org/x/net/http2"
)

func main() {
	if os.Getenv("BENCH_SERVER_PPROF") == "1" {
		go func() { _ = http.ListenAndServe("127.0.0.1:6060", nil) }()
	}

	selectedFramework := os.Getenv("FRAMEWORK")
	scenarios := []string{"simple", "json", "params"}
	// Note: Fiber doesn't support HTTP/2 (H2C), excluded from hybrid benchmark
	allFrameworks := []string{"nethttp", "gin", "echo", "chi", "iris", "celeris"}

	var frameworks []string
	if selectedFramework != "" {
		frameworks = []string{selectedFramework}
		fmt.Printf("Running benchmarks for: %s\n", selectedFramework)
	} else {
		frameworks = allFrameworks
		fmt.Println("Running benchmarks for all frameworks")
	}

	var results []client.RampUpResult
	for _, fw := range frameworks {
		fmt.Printf("\n╔════════════════════════════════════════════════════════════════════════════════╗\n")
		fmt.Printf("║ Testing Framework: %-60s ║\n", fw)
		fmt.Printf("╚════════════════════════════════════════════════════════════════════════════════╝\n")

		for _, sc := range scenarios {
			fmt.Printf("\n→ Scenario: %s\n", sc)

			// Start Server in Hybrid Mode
			srvHandle := server.Start(fw, sc, "hybrid")
			if srvHandle == nil {
				fmt.Printf("✗ FAILED to start server for %s\n", fw)
				continue
			}

			// Determine URL path
			path := "/bench"
			switch sc {
			case "json":
				path = "/json"
			case "params":
				path = "/users/123/posts/456"
			}
			url := "http://" + srvHandle.Addr + path
			if srvHandle.Addr[0] == ':' {
				url = "http://127.0.0.1" + srvHandle.Addr + path
			}

			// Profiling
			var profFile *os.File
			if fw == "celeris" && os.Getenv("BENCH_CPU_PROFILE") == "1" {
				f, err := os.Create("cpu-celeris-hybrid-" + sc + ".pprof")
				if err == nil {
					_ = pprof.StartCPUProfile(f)
					profFile = f
				}
			}

			// Setup Mixed Client (H1 and H2)
			// We'll use two types of workers: H1 and H2
			// But RunRampUp takes a single factory.
			// We'll create a "HybridWorker" that randomly chooses H1 or H2, or alternates.
			// Or better: RunRampUp adds clients. We can make the factory toggle between H1 and H2.

			// H1 Client
			h1Transport := &http.Transport{
				MaxIdleConns:        1000,
				MaxIdleConnsPerHost: 1000,
				IdleConnTimeout:     30 * time.Second,
			}
			h1Client := &http.Client{Transport: h1Transport, Timeout: client.TimeoutThresh}

			// H2 Client
			numH2Clients := 4
			if v := os.Getenv("H2_CLIENTS"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					numH2Clients = n
				}
			}
			trs := make([]http.RoundTripper, 0, numH2Clients)
			for i := 0; i < numH2Clients; i++ {
				trs = append(trs, &http2.Transport{
					AllowHTTP: true,
					DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
						return net.Dial(network, addr)
					},
				})
			}
			h2Client := &http.Client{
				Transport: &client.RoundRobinTransport{Transports: trs},
				Timeout:   client.TimeoutThresh,
			}

			// Factory that alternates between H1 and H2
			// We use a closure variable to toggle
			var clientCounter int

			r := client.RunRampUp(url, fw, sc, func() client.WorkerFunc {
				clientCounter++
				if clientCounter%2 == 0 {
					return client.StandardHTTPWorker(h2Client, url)
				}
				return client.StandardHTTPWorker(h1Client, url)
			})

			if profFile != nil {
				pprof.StopCPUProfile()
				_ = profFile.Close()
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srvHandle.Stop(ctx)
			cancel()

			results = append(results, r)

			fmt.Printf("✓ Complete: MaxClients=%d | MaxRPS=%.0f | P95=%.2fms | Time=%.1fs\n",
				r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)

			time.Sleep(1 * time.Second)
		}
	}

	client.PrintResults(results, "Sync Hybrid (H1+H2)")
	client.SaveResults(results, "sync_hybrid")
}
