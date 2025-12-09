// Package main benchmarks asynchronous hybrid HTTP.
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
	"strings"
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

			srvHandle := server.Start(fw, sc, "hybrid")
			if srvHandle == nil {
				fmt.Printf("✗ FAILED to start server for %s\n", fw)
				continue
			}

			path := "/bench"
			switch sc {
			case "json":
				path = "/json"
			case "params":
				path = "/users/123/posts/456"
			}

			addr := srvHandle.Addr
			host := "localhost"
			if strings.HasPrefix(addr, ":") {
				addr = "127.0.0.1" + addr
			} else {
				host, _, _ = net.SplitHostPort(addr)
			}
			url := "http://" + addr + path

			var profFile *os.File
			if fw == "celeris" && os.Getenv("BENCH_CPU_PROFILE") == "1" {
				f, err := os.Create("cpu-celeris-async-hybrid-" + sc + ".pprof")
				if err == nil {
					_ = pprof.StartCPUProfile(f)
					profFile = f
				}
			}

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

			// Hybrid Async Worker: Alternates between Pipelined H1 and Batch H2
			var clientCounter int
			r := client.RunRampUp(url, fw, sc, func() client.WorkerFunc {
				clientCounter++
				if clientCounter%2 == 0 {
					return client.BatchHTTP2Worker(h2Client, url, 10)
				}
				return client.PipelinedHTTP1Worker(addr, path, host, 10)
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

	client.PrintResults(results, "Async Hybrid (H1+H2)")
	client.SaveResults(results, "async_hybrid")
}
