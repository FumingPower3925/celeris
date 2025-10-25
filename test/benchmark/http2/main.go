// Package main provides the active ramp-up benchmark runner.
package main

import (
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"runtime"
	"runtime/pprof"

	_ "net/http/pprof"

	"github.com/albertbausili/celeris/pkg/celeris"
	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/kataras/iris/v12"
	"github.com/labstack/echo/v4"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// rrTransport implements http.RoundTripper and dispatches requests across
// multiple underlying http.RoundTrippers (each maintaining its own H2 conn).
type rrTransport struct {
	transports []http.RoundTripper
	idx        uint64
}

func (r *rrTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(r.transports) == 0 {
		return http.DefaultTransport.RoundTrip(req)
	}
	i := atomic.AddUint64(&r.idx, 1)
	t := r.transports[int(i)%len(r.transports)]
	return t.RoundTrip(req)
}

type RampUpResult struct {
	Framework     string  `json:"framework"`
	Scenario      string  `json:"scenario"`
	MaxClients    int     `json:"max_clients"`
	MaxRPS        float64 `json:"max_rps"`
	P95AtMax      float64 `json:"p95_at_max_ms"`
	TimeToDegrade float64 `json:"time_to_degrade_s"`
}

// ServerHandle represents a running server and how to stop it.
type ServerHandle struct {
	Addr string
	Stop func(context.Context) error
}

const (
	rampUpInterval    = 25 * time.Millisecond // Add 1 client every 25ms (40 clients/second!)
	measureWindow     = 1 * time.Second       // Measure p95 over 1s windows
	degradationThresh = 100.0                 // p95 > 100ms = degraded
	timeoutThresh     = 3 * time.Second       // Request timeout
	maxTestDuration   = 30 * time.Second      // Max test duration
	requestDelay      = 2 * time.Millisecond  // Minimal delay between requests per client
)

func runRampUpBenchmark() {
	// Optional: start server-side pprof endpoint
	if os.Getenv("BENCH_SERVER_PPROF") == "1" {
		go func() {
			_ = http.ListenAndServe("127.0.0.1:6060", nil)
		}()
	}
	// Check for FRAMEWORK environment variable to filter frameworks
	selectedFramework := os.Getenv("FRAMEWORK")

	scenarios := []string{"simple", "json", "params"}
	if os.Getenv("H2_ENABLE_PUSH") == "1" {
		scenarios = append(scenarios, "push")
	}
	allFrameworks := []string{"nethttp", "gin", "echo", "chi", "iris", "celeris"}

	var frameworks []string
	if selectedFramework != "" {
		// Run only the selected framework
		frameworks = []string{selectedFramework}
		fmt.Printf("Running benchmarks for: %s\n", selectedFramework)
	} else {
		// Run all frameworks
		frameworks = allFrameworks
		fmt.Println("Running benchmarks for all frameworks")
	}

	var results []RampUpResult
	for _, fw := range frameworks {
		fmt.Printf("\n╔════════════════════════════════════════════════════════════════════════════════╗\n")
		fmt.Printf("║ Testing Framework: %-60s ║\n", fw)
		fmt.Printf("╚════════════════════════════════════════════════════════════════════════════════╝\n")

		var fwResults []RampUpResult
		for _, sc := range scenarios {
			fmt.Printf("\n→ Scenario: %s\n", sc)
			srv, client := startServerHTTP2(fw, sc)
			if srv == nil {
				fmt.Printf("✗ FAILED to start server\n")
				continue
			}
			url := "http://localhost" + srv.Addr + scenarioPathStr(sc)
			// CPU profiling optionally enabled via env BENCH_CPU_PROFILE=1 for Celeris only
			var profFile *os.File
			if fw == "celeris" && os.Getenv("BENCH_CPU_PROFILE") == "1" {
				f, err := os.Create("cpu-celeris-" + sc + ".pprof")
				if err == nil {
					_ = pprof.StartCPUProfile(f)
					profFile = f
				}
			}

			r := rampUpTest(client, url, fw, sc)

			if profFile != nil {
				pprof.StopCPUProfile()
				_ = profFile.Close()
			}
			// profiling disabled
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Stop(ctx)
			cancel()
			results = append(results, r)
			fwResults = append(fwResults, r)

			// Print immediate result for this scenario
			fmt.Printf("✓ Complete: MaxClients=%d | MaxRPS=%.0f | P95=%.2fms | Time=%.1fs\n",
				r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)

			time.Sleep(1 * time.Second) // Cool down between tests
		}

		// Print framework summary
		fmt.Printf("\n┌─ %s Summary ─────────────────────────────────────────────────────────────┐\n", fw)
		fmt.Printf("│ %-10s │ %12s │ %12s │ %12s │ %12s │\n",
			"Scenario", "MaxClients", "MaxRPS", "P95(ms)", "Time(s)")
		fmt.Printf("├────────────┼──────────────┼──────────────┼──────────────┼──────────────┤\n")
		for _, r := range fwResults {
			fmt.Printf("│ %-10s │ %12d │ %12.0f │ %12.2f │ %12.1f │\n",
				r.Scenario, r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)
		}
		fmt.Printf("└──────────────────────────────────────────────────────────────────────────┘\n")
	}

	printRampUpResults(results)
	writeRampUpJSON(results)
	writeRampUpCSV(results)
	writeRampUpMarkdown(results)
}

type measurement struct {
	timestamp time.Time
	latencyNs float64
	success   bool
}

func rampUpTest(client *http.Client, url string, fw, sc string) RampUpResult {
	var mu sync.Mutex
	var measurements []measurement
	var activeClients int32
	stopChan := make(chan struct{})
	var wg sync.WaitGroup

	startTime := time.Now()
	maxClients := 0
	maxRPS := 0.0
	p95AtMax := 0.0
	degradeTime := 0.0

	// Ticker to add clients
	addClientTicker := time.NewTicker(rampUpInterval)
	defer addClientTicker.Stop()

	// Ticker to measure p95
	measureTicker := time.NewTicker(measureWindow)
	defer measureTicker.Stop()

	// Worker function with rate limiting
	workerFunc := func() {
		defer wg.Done()
		atomic.AddInt32(&activeClients, 1)
		defer atomic.AddInt32(&activeClients, -1)

		for {
			select {
			case <-stopChan:
				return
			default:
			}

			reqStart := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), timeoutThresh)
			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
			resp, err := client.Do(req)
			latency := time.Since(reqStart)
			cancel()

			success := err == nil && resp != nil && resp.StatusCode == 200

			if success {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			} else if resp != nil {
				_ = resp.Body.Close()
			}

			mu.Lock()
			measurements = append(measurements, measurement{
				timestamp: reqStart,
				latencyNs: float64(latency.Nanoseconds()),
				success:   success,
			})
			mu.Unlock()

			// Rate limit to avoid overwhelming the server
			time.Sleep(requestDelay)
		}
	}

	// Start ramping up
	testLoop := true
	emptyWindows := 0
	for testLoop {
		select {
		case <-addClientTicker.C:
			// Add a new client
			wg.Add(1)
			go workerFunc()

		case <-measureTicker.C:
			// Measure current p95
			mu.Lock()
			now := time.Now()
			cutoff := now.Add(-measureWindow)
			var recent []float64
			successCount := 0
			totalCount := 0
			for i := len(measurements) - 1; i >= 0; i-- {
				if measurements[i].timestamp.Before(cutoff) {
					break
				}
				totalCount++
				if measurements[i].success {
					recent = append(recent, measurements[i].latencyNs)
					successCount++
				}
			}
			mu.Unlock()

			clients := int(atomic.LoadInt32(&activeClients))

			if len(recent) == 0 {
				// Allow a few empty windows during ramp-up before failing
				emptyWindows++
				if clients > 20 && emptyWindows >= 3 {
					fmt.Printf("  ✗ FAILED: No successful requests with %d clients (after %d windows)\n", clients, emptyWindows)
					testLoop = false
				}
				continue
			} else {
				emptyWindows = 0
			}

			p95 := percentile(recent, 95)
			rps := float64(successCount) / measureWindow.Seconds()

			// Track max
			if rps > maxRPS {
				maxRPS = rps
				maxClients = clients
				p95AtMax = p95 / 1e6
				degradeTime = time.Since(startTime).Seconds()
			}

			// Check for degradation
			if p95/1e6 > degradationThresh {
				fmt.Printf("  ⚠ Degraded: p95=%.2fms (threshold=%.0fms)\n", p95/1e6, degradationThresh)
				testLoop = false
				// no break; loop will exit after next select tick due to flag
			}

			// Check for timeout
			if time.Since(startTime) > maxTestDuration {
				testLoop = false
				// no break; loop will exit after next select tick due to flag
			}
		}
	}

	// Stop all workers
	close(stopChan)
	wg.Wait()

	return RampUpResult{
		Framework:     fw,
		Scenario:      sc,
		MaxClients:    maxClients,
		MaxRPS:        maxRPS,
		P95AtMax:      p95AtMax,
		TimeToDegrade: degradeTime,
	}
}

// percentile returns the pth percentile from a slice of float64 values in nanoseconds.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return math.NaN()
	}
	sort.Float64s(vals)
	idx := int(math.Round((p / 100.0) * float64(len(vals)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}

func scenarioPathStr(s string) string {
	switch s {
	case "simple":
		return "/bench"
	case "json":
		return "/json"
	case "params":
		return "/users/123/posts/456"
	default:
		return "/bench"
	}
}

func startServerHTTP2(fw, sc string) (*ServerHandle, *http.Client) {
	switch fw {
	case "celeris":
		return startCelerisHTTP2(sc)
	case "nethttp":
		return startNetHTTPHTTP2(sc)
	case "gin":
		return startGinHTTP2(sc)
	case "echo":
		return startEchoHTTP2(sc)
	case "chi":
		return startChiHTTP2(sc)
	case "iris":
		return startIrisHTTP2(sc)
	default:
		return nil, nil
	}
}

func startCelerisHTTP2(sc string) (*ServerHandle, *http.Client) {
	r := celeris.NewRouter()
	switch sc {
	case "simple":
		r.GET("/bench", func(ctx *celeris.Context) error {
			// Demonstrate server push of a tiny resource (header-only push)
			// Pushed resource will be served by another handler below
			_ = ctx.PushPromise("/pushed.txt", map[string]string{"accept": "text/plain"})
			return ctx.String(200, "ok")
		})
		r.GET("/pushed.txt", func(ctx *celeris.Context) error { return ctx.String(200, "pushed") })
	case "json":
		r.GET("/json", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]any{"status": "ok", "code": 200})
		})
	case "params":
		r.GET("/users/:userId/posts/:postId", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{
				"user_id": celeris.Param(ctx, "userId"),
				"post_id": celeris.Param(ctx, "postId"),
			})
		})
	case "push":
		// Push multiple small resources and serve them
		r.GET("/bench", func(ctx *celeris.Context) error {
			_ = ctx.PushPromise("/pushed-a.txt", nil)
			_ = ctx.PushPromise("/pushed-b.txt", nil)
			_ = ctx.PushPromise("/pushed-c.txt", nil)
			return ctx.String(200, "ok")
		})
		r.GET("/pushed-a.txt", func(ctx *celeris.Context) error { return ctx.String(200, "A") })
		r.GET("/pushed-b.txt", func(ctx *celeris.Context) error { return ctx.String(200, "B") })
		r.GET("/pushed-c.txt", func(ctx *celeris.Context) error { return ctx.String(200, "C") })
	}
	cfg := celeris.DefaultConfig()
	// Auto-tune event loops to CPUs (leave headroom for client+OS)
	cpus := runtime.GOMAXPROCS(0)
	if cpus <= 2 {
		cfg.NumEventLoop = cpus
	} else if cpus <= 8 {
		cfg.NumEventLoop = cpus - 1
	} else {
		cfg.NumEventLoop = cpus - 2
	}
	cfg.Multicore = true // Enable multicore for maximum performance
	cfg.ReusePort = true // Let gnet accept on all loops when supported
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.Addr = freePort()
	cfg.EnableH1 = false // HTTP/2 only
	cfg.EnableH2 = true
	// Increase concurrency significantly to saturate server under test
	cfg.MaxConcurrentStreams = 2000
	srv := celeris.New(cfg)
	go func() { _ = srv.ListenAndServe(r) }()
	time.Sleep(500 * time.Millisecond)
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
	client := &http.Client{
		Transport: &rrTransport{transports: trs},
		Timeout:   timeoutThresh,
	}
	return &ServerHandle{Addr: cfg.Addr, Stop: srv.Stop}, client
}

func startNetHTTPHTTP2(sc string) (*ServerHandle, *http.Client) {
	mux := http.NewServeMux()
	switch sc {
	case "simple":
		mux.HandleFunc("/bench", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
	case "json":
		mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"status\":\"ok\",\"code\":200}"))
		})
	case "params":
		mux.HandleFunc("/users/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"user_id\":\"123\",\"post_id\":\"456\"}"))
		})
	}
	h2s := &http2.Server{}
	handler := h2c.NewHandler(mux, h2s)
	srv := &http.Server{Addr: freePort(), Handler: handler}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
		Timeout: timeoutThresh,
	}
	return &ServerHandle{Addr: srv.Addr, Stop: srv.Shutdown}, client
}

func startGinHTTP2(sc string) (*ServerHandle, *http.Client) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	switch sc {
	case "simple":
		r.GET("/bench", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	case "json":
		r.GET("/json", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "code": 200})
		})
	case "params":
		r.GET("/users/:userId/posts/:postId", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"user_id": c.Param("userId"),
				"post_id": c.Param("postId"),
			})
		})
	}
	h2s := &http2.Server{}
	handler := h2c.NewHandler(r, h2s)
	srv := &http.Server{Addr: freePort(), Handler: handler}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
		Timeout: timeoutThresh,
	}
	return &ServerHandle{Addr: srv.Addr, Stop: srv.Shutdown}, client
}

func startEchoHTTP2(sc string) (*ServerHandle, *http.Client) {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	switch sc {
	case "simple":
		e.GET("/bench", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	case "json":
		e.GET("/json", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]any{"status": "ok", "code": 200})
		})
	case "params":
		e.GET("/users/:userId/posts/:postId", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{
				"user_id": c.Param("userId"),
				"post_id": c.Param("postId"),
			})
		})
	}
	addr := freePort()

	h2s := &http2.Server{}
	handler := h2c.NewHandler(e, h2s)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addrStr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addrStr)
			},
		},
		Timeout: timeoutThresh,
	}
	return &ServerHandle{Addr: addr, Stop: func(ctx context.Context) error { return srv.Shutdown(ctx) }}, client
}

func startChiHTTP2(sc string) (*ServerHandle, *http.Client) {
	r := chi.NewRouter()
	switch sc {
	case "simple":
		r.Get("/bench", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
	case "json":
		r.Get("/json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"status\":\"ok\",\"code\":200}"))
		})
	case "params":
		r.Get("/users/{userId}/posts/{postId}", func(w http.ResponseWriter, req *http.Request) {
			_, _ = fmt.Fprintf(w, "{\"user_id\":\"%s\",\"post_id\":\"%s\"}",
				chi.URLParam(req, "userId"), chi.URLParam(req, "postId"))
		})
	}
	h2s := &http2.Server{}
	handler := h2c.NewHandler(r, h2s)
	srv := &http.Server{Addr: freePort(), Handler: handler}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
		Timeout: timeoutThresh,
	}
	return &ServerHandle{Addr: srv.Addr, Stop: srv.Shutdown}, client
}

func startIrisHTTP2(sc string) (*ServerHandle, *http.Client) {
	app := iris.New()
	app.Configure(iris.WithoutStartupLog, iris.WithOptimizations)
	switch sc {
	case "simple":
		app.Get("/bench", func(ctx iris.Context) {
			_, _ = ctx.WriteString("ok")
		})
	case "json":
		app.Get("/json", func(ctx iris.Context) {
			_ = ctx.JSON(iris.Map{"status": "ok", "code": 200})
		})
	case "params":
		app.Get("/users/{userId}/posts/{postId}", func(ctx iris.Context) {
			_ = ctx.JSON(iris.Map{
				"user_id": ctx.Params().Get("userId"),
				"post_id": ctx.Params().Get("postId"),
			})
		})
	}

	// Build the app first
	if err := app.Build(); err != nil {
		panic(err)
	}

	addr := freePort()
	h2s := &http2.Server{}
	handler := h2c.NewHandler(app, h2s)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addrStr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addrStr)
			},
		},
		Timeout: timeoutThresh,
	}
	return &ServerHandle{Addr: addr, Stop: func(ctx context.Context) error { return srv.Shutdown(ctx) }}, client
}

// freePort returns an available TCP port as a ":port" string.
func freePort() string {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()
	i := strings.LastIndex(addr, ":")
	if i == -1 {
		return ":0"
	}
	return addr[i:]
}

func printRampUpResults(results []RampUpResult) {
	fmt.Printf("\n\n=== RAMP-UP BENCHMARK RESULTS ===\n\n")

	// Group by scenario
	m := make(map[string][]RampUpResult)
	for _, r := range results {
		m[r.Scenario] = append(m[r.Scenario], r)
	}

	for _, sc := range []string{"simple", "json", "params"} {
		rows := m[sc]
		if len(rows) == 0 {
			continue
		}
		fmt.Printf("\nScenario: %s\n", sc)
		fmt.Printf("%-10s | %12s | %12s | %12s | %12s\n",
			"Framework", "Max Clients", "Max RPS", "P95 @ Max", "Time (s)")
		fmt.Printf("%-10s-|-%12s-|-%12s-|-%12s-|-%12s\n",
			"----------", "------------", "------------", "------------", "------------")

		sort.Slice(rows, func(i, j int) bool {
			return rows[i].MaxRPS > rows[j].MaxRPS
		})

		for _, r := range rows {
			fmt.Printf("%-10s | %12d | %12.0f | %9.2f ms | %12.1f\n",
				r.Framework, r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)
		}
	}
}

func writeRampUpJSON(results []RampUpResult) {
	f, _ := os.Create("rampup_results.json")
	defer f.Close()
	_ = json.NewEncoder(f).Encode(results)
}

func writeRampUpCSV(results []RampUpResult) {
	f, _ := os.Create("rampup_results.csv")
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"scenario", "framework", "max_clients", "max_rps", "p95_at_max_ms", "time_to_degrade_s"})
	for _, r := range results {
		_ = w.Write([]string{
			r.Scenario,
			r.Framework,
			strconv.Itoa(r.MaxClients),
			fmt.Sprintf("%.0f", r.MaxRPS),
			fmt.Sprintf("%.2f", r.P95AtMax),
			fmt.Sprintf("%.1f", r.TimeToDegrade),
		})
	}
}

func writeRampUpMarkdown(results []RampUpResult) {
	f, _ := os.Create("rampup_results.md")
	defer f.Close()

	_, _ = fmt.Fprintln(f, "# Ramp-Up Benchmark Results")
	_, _ = fmt.Fprintln(f, "")
	_, _ = fmt.Fprintln(f, "**Test Methodology**: Gradually increase concurrent clients (1 client every 100ms) until p95 latency exceeds 100ms or timeout.")
	_, _ = fmt.Fprintln(f, "")

	m := make(map[string][]RampUpResult)
	for _, r := range results {
		m[r.Scenario] = append(m[r.Scenario], r)
	}

	for _, sc := range []string{"simple", "json", "params"} {
		rows := m[sc]
		if len(rows) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(f, "\n## Scenario: %s\n\n", sc)
		_, _ = fmt.Fprintln(f, "| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |")
		_, _ = fmt.Fprintln(f, "|-----------|------------:|--------:|---------------:|--------------------:|")

		sort.Slice(rows, func(i, j int) bool {
			return rows[i].MaxRPS > rows[j].MaxRPS
		})

		for _, r := range rows {
			_, _ = fmt.Fprintf(f, "| %s | %d | %.0f | %.2f | %.1f |\n",
				r.Framework, r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)
		}
	}
}

func main() {
	runRampUpBenchmark()
}

// Helper functions percentile, freePort, and ServerHandle are defined in main.go
