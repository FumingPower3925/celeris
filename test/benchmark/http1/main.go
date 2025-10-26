// Package main provides HTTP/1.1 ramp-up benchmark across multiple frameworks.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gofiber/fiber/v2"
	"github.com/labstack/echo/v4"
)

// rrTransport implements http.RoundTripper and dispatches requests across
// multiple underlying http.RoundTrippers (each maintaining its own conn pool).
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

func main() {
	// Optional: start server-side pprof endpoint for the benchmark process
	if os.Getenv("BENCH_SERVER_PPROF") == "1" {
		go func() { _ = http.ListenAndServe("127.0.0.1:6060", nil) }()
	}
	// Check for FRAMEWORK environment variable to filter frameworks
	selectedFramework := os.Getenv("FRAMEWORK")

	scenarios := []string{"simple", "json", "params"}
	allFrameworks := []string{"celeris", "nethttp", "gin", "echo", "chi", "fiber"}

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
			srv, client := startServerHTTP1(fw, sc)
			if srv == nil {
				fmt.Printf("✗ FAILED to start server\n")
				continue
			}
			url := "http://" + srv.Addr + scenarioPathStr(sc)

			// Warm up server and ensure 200 OK is reachable
			_ = warmup(client, url)

			// Optional CPU profiling for Celeris only (enable with BENCH_CPU_PROFILE=1)
			var profFile *os.File
			if fw == "celeris" && os.Getenv("BENCH_CPU_PROFILE") == "1" {
				f, err := os.Create("cpu-h1-celeris-" + sc + ".pprof")
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

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Stop(ctx)
			cancel()
			results = append(results, r)
			fwResults = append(fwResults, r)

			// Print immediate result for this scenario
			fmt.Printf("✓ Complete: MaxClients=%d | MaxRPS=%.0f | P95=%.2fms | Time=%.1fs\n",
				r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)

			time.Sleep(500 * time.Millisecond) // Cool down between tests
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
	saveRampUpResults(results)
}

func scenarioPathStr(sc string) string {
	switch sc {
	case "simple":
		return "/"
	case "json":
		return "/json"
	case "params":
		return "/user/123/post/456"
	default:
		return "/"
	}
}

func startServerHTTP1(framework, scenario string) (*ServerHandle, *http.Client) {
	addr := "127.0.0.1" + freePort()

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
	client := &http.Client{
		Timeout:   timeoutThresh,
		Transport: &rrTransport{transports: trs},
	}

	switch framework {
	case "nethttp":
		return startNetHTTP(addr, scenario), client
	case "gin":
		return startGin(addr, scenario), client
	case "echo":
		return startEcho(addr, scenario), client
	case "chi":
		return startChi(addr, scenario), client
	case "fiber":
		return startFiber(addr, scenario), client
	case "celeris":
		return startCelerisHTTP1(addr, scenario), client
	default:
		return nil, nil
	}
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

func warmup(client *http.Client, url string) error {
	for i := 0; i < 5; i++ {
		resp, err := client.Get(url)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == 200 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func startNetHTTP(addr, scenario string) *ServerHandle {
	mux := http.NewServeMux()
	switch scenario {
	case "simple":
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("Hello, World!"))
		})
	case "json":
		mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"message":"Hello, World!"}`))
		})
	case "params":
		mux.HandleFunc("/user/", func(w http.ResponseWriter, r *http.Request) {
			// very simple param extraction: /user/123/post/456
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 5 && parts[1] == "user" && parts[3] == "post" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"userId":"%s","postId":"%s"}`, parts[2], parts[4])))
				return
			}
			http.NotFound(w, r)
		})
	}
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)
	return &ServerHandle{Addr: addr, Stop: srv.Shutdown}
}

func startGin(addr, scenario string) *ServerHandle {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.SetTrustedProxies(nil)
	switch scenario {
	case "simple":
		r.GET("/", func(c *gin.Context) { c.String(200, "Hello, World!") })
	case "json":
		r.GET("/json", func(c *gin.Context) { c.JSON(200, gin.H{"message": "Hello, World!"}) })
	case "params":
		r.GET("/user/:userId/post/:postId", func(c *gin.Context) {
			c.JSON(200, gin.H{"userId": c.Param("userId"), "postId": c.Param("postId")})
		})
	}
	srv := &http.Server{Addr: addr, Handler: r}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)
	return &ServerHandle{Addr: addr, Stop: srv.Shutdown}
}

func startEcho(addr, scenario string) *ServerHandle {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	switch scenario {
	case "simple":
		e.GET("/", func(c echo.Context) error { return c.String(200, "Hello, World!") })
	case "json":
		e.GET("/json", func(c echo.Context) error { return c.JSON(200, map[string]string{"message": "Hello, World!"}) })
	case "params":
		e.GET("/user/:userId/post/:postId", func(c echo.Context) error {
			return c.JSON(200, map[string]string{"userId": c.Param("userId"), "postId": c.Param("postId")})
		})
	}
	go func() { _ = e.Start(addr) }()
	time.Sleep(100 * time.Millisecond)
	return &ServerHandle{Addr: addr, Stop: func(ctx context.Context) error { return e.Shutdown(ctx) }}
}

func startChi(addr, scenario string) *ServerHandle {
	r := chi.NewRouter()
	switch scenario {
	case "simple":
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("Hello, World!"))
		})
	case "json":
		r.Get("/json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"message":"Hello, World!"}`))
		})
	case "params":
		r.Get("/user/{userId}/post/{postId}", func(w http.ResponseWriter, req *http.Request) {
			userId := chi.URLParam(req, "userId")
			postId := chi.URLParam(req, "postId")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"userId":"%s","postId":"%s"}`, userId, postId)))
		})
	}
	srv := &http.Server{Addr: addr, Handler: r}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)
	return &ServerHandle{Addr: addr, Stop: srv.Shutdown}
}

func startFiber(addr, scenario string) *ServerHandle {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	switch scenario {
	case "simple":
		app.Get("/", func(c *fiber.Ctx) error {
			return c.SendString("Hello, World!")
		})
	case "json":
		app.Get("/json", func(c *fiber.Ctx) error {
			return c.JSON(fiber.Map{"message": "Hello, World!"})
		})
	case "params":
		app.Get("/user/:userId/post/:postId", func(c *fiber.Ctx) error {
			return c.JSON(fiber.Map{
				"userId": c.Params("userId"),
				"postId": c.Params("postId"),
			})
		})
	}

	go func() {
		_ = app.Listen(addr)
	}()

	time.Sleep(100 * time.Millisecond)

	return &ServerHandle{
		Addr: addr,
		Stop: func(ctx context.Context) error {
			return app.Shutdown()
		},
	}
}

func startCelerisHTTP1(addr, scenario string) *ServerHandle {
	router := celeris.NewRouter()

	switch scenario {
	case "simple":
		router.GET("/", func(ctx *celeris.Context) error {
			return ctx.String(200, "Hello, World!")
		})
	case "json":
		router.GET("/json", func(ctx *celeris.Context) error {
			// Avoid encoding overhead in benchmark path
			return ctx.Data(200, "application/json", []byte(`{"message":"Hello, World!"}`))
		})
	case "params":
		router.GET("/user/:userId/post/:postId", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{
				"userId": ctx.Param("userId"),
				"postId": ctx.Param("postId"),
			})
		})
	}

	config := celeris.DefaultConfig()
	config.Addr = addr
	config.EnableH1 = true // HTTP/1.1 only
	config.EnableH2 = false
	// Silence server logs for clean benchmark output
	config.Logger = log.New(io.Discard, "", 0)
	// Explicit gnet tuning
	cpus := runtime.GOMAXPROCS(0)
	if cpus <= 2 {
		config.NumEventLoop = cpus
	} else if cpus <= 8 {
		config.NumEventLoop = cpus - 1
	} else {
		config.NumEventLoop = cpus - 2
	}
	config.Multicore = true
	config.ReusePort = true

	server := celeris.New(config)

	go func() {
		_ = server.ListenAndServe(router)
	}()

	time.Sleep(100 * time.Millisecond)

	return &ServerHandle{
		Addr: addr,
		Stop: func(ctx context.Context) error {
			return server.Stop(ctx)
		},
	}
}

func rampUpTest(client *http.Client, url, framework, scenario string) RampUpResult {
	var activeClients atomic.Int32
	var totalRequests atomic.Int64
	var recentLatencies sync.Map // map[int64][]float64 keyed by second

	stopChan := make(chan struct{})
	var wg sync.WaitGroup

	// Worker function
	workerFunc := func() {
		defer wg.Done()
		defer activeClients.Add(-1)

		for {
			select {
			case <-stopChan:
				return
			default:
			}

			start := time.Now()
			resp, err := client.Get(url)
			latency := time.Since(start).Seconds() * 1000 // ms

			if err == nil && resp.StatusCode == 200 {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				totalRequests.Add(1)

				// Record latency
				second := time.Now().Unix()
				latSlice, _ := recentLatencies.LoadOrStore(second, &sync.Mutex{})
				mu := latSlice.(*sync.Mutex)
				mu.Lock()
				key := fmt.Sprintf("%d-data", second)
				data, _ := recentLatencies.LoadOrStore(key, []float64{})
				dataSlice := data.([]float64)
				dataSlice = append(dataSlice, latency)
				recentLatencies.Store(key, dataSlice)
				mu.Unlock()
			} else if framework == "celeris" && scenario == "json" && err == nil {
				// Minimal debug: observe non-200 statuses for Celeris JSON (first few only)
				// Avoiding heavy logs
				_ = resp.Body.Close()
			}

			time.Sleep(requestDelay)
		}
	}

	// Ramp up clients
	ticker := time.NewTicker(rampUpInterval)
	defer ticker.Stop()

	startTime := time.Now()
	maxClients := 0
	maxRPS := 0.0
	p95AtMax := 0.0
	var timeToDegrade float64

	measureTicker := time.NewTicker(measureWindow)
	defer measureTicker.Stop()

	lastCount := totalRequests.Load()
	lastTime := time.Now()

	go func() {
		for range measureTicker.C {
			currentCount := totalRequests.Load()
			currentTime := time.Now()
			elapsed := currentTime.Sub(lastTime).Seconds()
			rps := float64(currentCount-lastCount) / elapsed

			// Calculate p95 for current window
			second := currentTime.Unix()
			key := fmt.Sprintf("%d-data", second-1) // Previous second
			if data, ok := recentLatencies.Load(key); ok {
				latencies := data.([]float64)
				if len(latencies) > 0 {
					p95 := percentile(latencies, 0.95)

					clients := int(activeClients.Load())
					if rps > maxRPS {
						maxRPS = rps
						maxClients = clients
						p95AtMax = p95
					}

					if p95 > degradationThresh {
						timeToDegrade = time.Since(startTime).Seconds()
						close(stopChan)
						return
					}
				}
			}

			lastCount = currentCount
			lastTime = currentTime
		}
	}()

	// Ramp up loop
	for {
		select {
		case <-ticker.C:
			if time.Since(startTime) > maxTestDuration {
				close(stopChan)
				goto done
			}
			activeClients.Add(1)
			wg.Add(1)
			go workerFunc()
		case <-stopChan:
			goto done
		}
	}

done:
	wg.Wait()

	if timeToDegrade == 0 {
		timeToDegrade = maxTestDuration.Seconds()
	}

	return RampUpResult{
		Framework:     framework,
		Scenario:      scenario,
		MaxClients:    maxClients,
		MaxRPS:        maxRPS,
		P95AtMax:      p95AtMax,
		TimeToDegrade: timeToDegrade,
	}
}

func percentile(data []float64, p float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)
	idx := int(math.Ceil(float64(len(sorted)) * p))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func printRampUpResults(results []RampUpResult) {
	fmt.Println("\n=== HTTP/1.1 Ramp-Up Benchmark Results ===")
	fmt.Printf("%-15s %-10s %12s %12s %12s %15s\n", "Framework", "Scenario", "MaxClients", "MaxRPS", "P95(ms)", "TimeToDegr(s)")
	fmt.Println(strings.Repeat("-", 85))
	for _, r := range results {
		fmt.Printf("%-15s %-10s %12d %12.0f %12.2f %15.1f\n",
			r.Framework, r.Scenario, r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)
	}
}

func saveRampUpResults(results []RampUpResult) {
	// JSON
	f, err := os.Create("rampup_results_h1.json")
	if err == nil {
		defer f.Close()
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
	}

	// CSV
	csvF, err := os.Create("rampup_results_h1.csv")
	if err == nil {
		defer csvF.Close()
		w := csv.NewWriter(csvF)
		_ = w.Write([]string{"Framework", "Scenario", "MaxClients", "MaxRPS", "P95(ms)", "TimeToDegrade(s)"})
		for _, r := range results {
			_ = w.Write([]string{
				r.Framework,
				r.Scenario,
				strconv.Itoa(r.MaxClients),
				fmt.Sprintf("%.0f", r.MaxRPS),
				fmt.Sprintf("%.2f", r.P95AtMax),
				fmt.Sprintf("%.1f", r.TimeToDegrade),
			})
		}
		w.Flush()
	}

	// Markdown
	mdF, err := os.Create("rampup_results_h1.md")
	if err == nil {
		defer mdF.Close()
		fmt.Fprintln(mdF, "# HTTP/1.1 Ramp-Up Benchmark Results")
		fmt.Fprintln(mdF, "")
		fmt.Fprintln(mdF, "| Framework | Scenario | MaxClients | MaxRPS | P95(ms) | TimeToDegrade(s) |")
		fmt.Fprintln(mdF, "|-----------|----------|------------|--------|---------|------------------|")
		for _, r := range results {
			fmt.Fprintf(mdF, "| %s | %s | %d | %.0f | %.2f | %.1f |\n",
				r.Framework, r.Scenario, r.MaxClients, r.MaxRPS, r.P95AtMax, r.TimeToDegrade)
		}
	}

	log.Println("Results saved to rampup_results_h1.{json,csv,md}")
}
