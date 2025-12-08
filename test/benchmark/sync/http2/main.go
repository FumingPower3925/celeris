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
	// Optional: start server-side pprof endpoint
	if os.Getenv("BENCH_SERVER_PPROF") == "1" {
		go func() {
			_ = http.ListenAndServe("127.0.0.1:6060", nil)
		}()
	}

	selectedFramework := os.Getenv("FRAMEWORK")
	scenarios := []string{"simple", "json", "params"}
	if os.Getenv("H2_ENABLE_PUSH") == "1" {
		scenarios = append(scenarios, "push")
	}

	// Note: Fiber doesn't support HTTP/2 (H2C), excluded from this benchmark
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

			// Start Server
			srvHandle := server.Start(fw, sc, "http2")
			if srvHandle == nil {
				fmt.Printf("✗ FAILED to start server for %s\n", fw)
				continue
			}

			// Setup Client
			// Support multiple parallel H2 connections via env H2_CLIENTS (default 4)
			numClients := 4
			if v := os.Getenv("H2_CLIENTS"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					numClients = n
				}
			}

			trs := make([]http.RoundTripper, 0, numClients)
			for i := 0; i < numClients; i++ {
				trs = append(trs, &http2.Transport{
					AllowHTTP: true,
					DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
						return net.Dial(network, addr)
					},
				})
			}
			httpClient := &http.Client{
				Transport: &client.RoundRobinTransport{Transports: trs},
				Timeout:   client.TimeoutThresh,
			}

			// Determine URL path
			path := "/bench"
			if sc == "json" {
				path = "/json"
			} else if sc == "params" {
				path = "/users/123/posts/456"
			}
			url := "http://" + srvHandle.Addr + path
			if srvHandle.Addr[0] == ':' {
				url = "http://127.0.0.1" + srvHandle.Addr + path
			}

			// Profiling
			var profFile *os.File
			if fw == "celeris" && os.Getenv("BENCH_CPU_PROFILE") == "1" {
				f, err := os.Create("cpu-celeris-" + sc + ".pprof")
				if err == nil {
					_ = pprof.StartCPUProfile(f)
					profFile = f
				}
			}

			// Run Test
			r := client.RunRampUp(url, fw, sc, func() client.WorkerFunc {
				return client.StandardHTTPWorker(httpClient, url)
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

	client.PrintResults(results, "Sync HTTP/2")
	client.SaveResults(results, "sync_h2")
}
