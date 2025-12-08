package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/pprof"
	"strings"
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

			// Determine URL path and Host
			path := "/bench"
			if sc == "json" {
				path = "/json"
			} else if sc == "params" {
				path = "/users/123/posts/456"
			}

			addr := srvHandle.Addr
			host := "localhost"
			if strings.HasPrefix(addr, ":") {
				addr = "127.0.0.1" + addr
			} else {
				host, _, _ = net.SplitHostPort(addr)
			}

			// Profiling
			var profFile *os.File
			if fw == "celeris" && os.Getenv("BENCH_CPU_PROFILE") == "1" {
				f, err := os.Create("cpu-celeris-async-h1-" + sc + ".pprof")
				if err == nil {
					_ = pprof.StartCPUProfile(f)
					profFile = f
				}
			}

			// Run Test with Pipelined Worker
			// Batch size 10
			r := client.RunRampUp("http://"+addr+path, fw, sc, func() client.WorkerFunc {
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

	client.PrintResults(results, "Async HTTP/1.1 (Pipelined)")
	client.SaveResults(results, "async_h1")
}
