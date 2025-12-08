package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/albertbausili/celeris/test/benchmark/internal/client"
	"github.com/albertbausili/celeris/test/benchmark/internal/server"
)

func main() {
	if os.Getenv("BENCH_SERVER_PPROF") == "1" {
		go func() { _ = http.ListenAndServe("127.0.0.1:6060", nil) }()
	}

	selectedFramework := os.Getenv("FRAMEWORK")
	scenarios := []string{"simple", "json", "params"}
	allFrameworks := []string{"nethttp", "gin", "echo", "chi", "iris", "fiber", "celeris"}

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

			srvHandle := server.Start(fw, sc, "http1")
			if srvHandle == nil {
				fmt.Printf("✗ FAILED to start server for %s\n", fw)
				continue
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
				f, err := os.Create("cpu-celeris-h1-" + sc + ".pprof")
				if err == nil {
					_ = pprof.StartCPUProfile(f)
					profFile = f
				}
			}

			// Setup Client
			// Use a standard HTTP client with appropriate timeout and transport settings
			// Build configurable client(s)
			base := &http.Transport{
				MaxIdleConns:        10000,
				MaxIdleConnsPerHost: 10000,
				MaxConnsPerHost:     10000,
				DisableCompression:  true,
				IdleConnTimeout:     90 * time.Second,
			}

			numClients := 4
			if v := os.Getenv("H1_CLIENTS"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					numClients = n
				}
			}

			trs := make([]http.RoundTripper, 0, numClients)
			for i := 0; i < numClients; i++ {
				// Clone base transport per client to avoid global conn pool contention
				t := &http.Transport{
					MaxIdleConns:        base.MaxIdleConns,
					MaxIdleConnsPerHost: base.MaxIdleConnsPerHost,
					MaxConnsPerHost:     base.MaxConnsPerHost,
					DisableCompression:  base.DisableCompression,
					IdleConnTimeout:     base.IdleConnTimeout,
				}
				trs = append(trs, t)
			}

			httpClient := &http.Client{
				Transport: &client.RoundRobinTransport{Transports: trs},
				Timeout:   client.TimeoutThresh,
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

	client.PrintResults(results, "Sync HTTP/1.1")
	client.SaveResults(results, "sync_h1")
}
