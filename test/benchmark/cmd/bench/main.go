//go:build benchlegacy

// Package main contains a legacy benchmark runner kept for reference.
// This file is excluded from normal builds by the benchlegacy build tag.
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
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gofiber/fiber/v2"
	"github.com/labstack/echo/v4"
	"golang.org/x/net/http2"
)

type Scenario string

const (
	ScenarioSimple Scenario = "simple"
	ScenarioJSON   Scenario = "json"
	ScenarioParams Scenario = "params"
)

type Framework string

const (
	FWCeleris Framework = "celeris"
	FWNetHTTP Framework = "nethttp"
	FWFiber   Framework = "fiber"
	FWGin     Framework = "gin"
	FWEcho    Framework = "echo"
	FWChi     Framework = "chi"
)

type Result struct {
	Framework   Framework `json:"framework"`
	Scenario    Scenario  `json:"scenario"`
	Concurrency int       `json:"concurrency"`
	RPS         float64   `json:"rps"`
	LatencyNs   float64   `json:"latency_ns_p50"`
}

// NOTE: ServerHandle type is defined in rampup.go for the active runner.

func main() {
	// Delegate to ramp-up runner
	runRampUpBenchmark()
}

type RunMetrics struct {
	RPS       float64
	LatencyNs float64
}

func runLoad(client *http.Client, url string, concurrency int, duration time.Duration) (RunMetrics, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var count int64
	var latencies []float64
	stop := time.Now().Add(duration)

	worker := func() {
		defer wg.Done()
		for time.Now().Before(stop) {
			start := time.Now()
			resp, err := client.Get(url)
			if err != nil {
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			mu.Lock()
			count++
			latencies = append(latencies, float64(time.Since(start).Nanoseconds()))
			mu.Unlock()
		}
	}

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go worker()
	}
	wg.Wait()

	d := duration.Seconds()
	rps := float64(count) / d
	p50 := percentile(latencies, 50)
	return RunMetrics{RPS: rps, LatencyNs: p50}, nil
}

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

func scenarioPath(s Scenario) string {
	switch s {
	case ScenarioSimple:
		return "/bench"
	case ScenarioJSON:
		return "/json"
	case ScenarioParams:
		return "/users/123/posts/456"
	default:
		return "/bench"
	}
}

func startServer(fw Framework, s Scenario) (*ServerHandle, *http.Client) {
	switch fw {
	case FWCeleris:
		return startCeleris(s)
	case FWNetHTTP:
		return startNetHTTP(s)
	case FWFiber:
		return startFiber(s)
	case FWGin:
		return startGin(s)
	case FWEcho:
		return startEcho(s)
	case FWChi:
		return startChi(s)
	default:
		return nil, nil
	}
}

func startCeleris(s Scenario) (*ServerHandle, *http.Client) {
	r := celeris.NewRouter()
	switch s {
	case ScenarioSimple:
		r.GET("/bench", func(ctx *celeris.Context) error { return ctx.String(200, "ok") })
	case ScenarioJSON:
		r.GET("/json", func(ctx *celeris.Context) error { return ctx.JSON(200, map[string]any{"status": "ok", "code": 200}) })
	case ScenarioParams:
		r.GET("/users/:userId/posts/:postId", func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]string{"user_id": celeris.Param(ctx, "userId"), "post_id": celeris.Param(ctx, "postId")})
		})
	}
	cfg := celeris.DefaultConfig()
	cfg.Multicore = false
	cfg.NumEventLoop = 1
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.Addr = freePort()
	srv := celeris.New(cfg)
	go func() { _ = srv.ListenAndServe(r) }()
	time.Sleep(400 * time.Millisecond)
	client := &http.Client{Transport: &http2.Transport{AllowHTTP: true, DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) { return net.Dial(network, addr) }}, Timeout: 5 * time.Second}
	return &ServerHandle{Addr: cfg.Addr, Stop: srv.Stop}, client
}

func startNetHTTP(s Scenario) (*ServerHandle, *http.Client) {
	mux := http.NewServeMux()
	switch s {
	case ScenarioSimple:
		mux.HandleFunc("/bench", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	case ScenarioJSON:
		mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"status\":\"ok\",\"code\":200}"))
		})
	case ScenarioParams:
		mux.HandleFunc("/users/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"user_id\":\"123\",\"post_id\":\"456\"}"))
		})
	}
	srv := &http.Server{Addr: freePort(), Handler: mux}
	_ = http2.ConfigureServer(srv, &http2.Server{})
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}
	return &ServerHandle{Addr: srv.Addr, Stop: srv.Shutdown}, client
}

func startFiber(s Scenario) (*ServerHandle, *http.Client) {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	switch s {
	case ScenarioSimple:
		app.Get("/bench", func(c *fiber.Ctx) error { return c.SendString("ok") })
	case ScenarioJSON:
		app.Get("/json", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok", "code": 200}) })
	case ScenarioParams:
		app.Get("/users/:userId/posts/:postId", func(c *fiber.Ctx) error {
			return c.JSON(fiber.Map{"user_id": c.Params("userId"), "post_id": c.Params("postId")})
		})
	}
	addr := freePort()
	go func() { _ = app.Listen(addr) }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}
	return &ServerHandle{Addr: addr, Stop: func(_ context.Context) error { return app.Shutdown() }}, client
}

func startGin(s Scenario) (*ServerHandle, *http.Client) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	switch s {
	case ScenarioSimple:
		r.GET("/bench", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	case ScenarioJSON:
		r.GET("/json", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok", "code": 200}) })
	case ScenarioParams:
		r.GET("/users/:userId/posts/:postId", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"user_id": c.Param("userId"), "post_id": c.Param("postId")})
		})
	}
	srv := &http.Server{Addr: freePort(), Handler: r}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}
	return &ServerHandle{Addr: srv.Addr, Stop: srv.Shutdown}, client
}

func startEcho(s Scenario) (*ServerHandle, *http.Client) {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	switch s {
	case ScenarioSimple:
		e.GET("/bench", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	case ScenarioJSON:
		e.GET("/json", func(c echo.Context) error { return c.JSON(http.StatusOK, map[string]any{"status": "ok", "code": 200}) })
	case ScenarioParams:
		e.GET("/users/:userId/posts/:postId", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"user_id": c.Param("userId"), "post_id": c.Param("postId")})
		})
	}
	addr := freePort()
	go func() { _ = e.Start(addr) }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}
	return &ServerHandle{Addr: addr, Stop: e.Shutdown}, client
}

func startChi(s Scenario) (*ServerHandle, *http.Client) {
	r := chi.NewRouter()
	switch s {
	case ScenarioSimple:
		r.Get("/bench", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	case ScenarioJSON:
		r.Get("/json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"status\":\"ok\",\"code\":200}"))
		})
	case ScenarioParams:
		r.Get("/users/{userId}/posts/{postId}", func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "{\"user_id\":\"%s\",\"post_id\":\"%s\"}", chi.URLParam(r, "userId"), chi.URLParam(r, "postId"))
		})
	}
	srv := &http.Server{Addr: freePort(), Handler: r}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}
	return &ServerHandle{Addr: srv.Addr, Stop: srv.Shutdown}, client
}

func freePort() string {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	return ln.Addr().String()[strings.LastIndex(ln.Addr().String(), ":"):]
}

func writeJSON(results []Result) {
	f, _ := os.Create("results.json")
	defer f.Close()
	_ = json.NewEncoder(f).Encode(results)
}

func printTable(results []Result) {
	m := map[Scenario][]Result{}
	for _, r := range results {
		m[r.Scenario] = append(m[r.Scenario], r)
	}
	for _, sc := range []Scenario{ScenarioSimple, ScenarioJSON, ScenarioParams} {
		fmt.Printf("\nScenario: %s\n", sc)
		fmt.Printf("%-10s %-6s %-12s %-12s\n", "Framework", "Conc", "RPS", "p50 (ms)")
		rows := m[sc]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Framework == rows[j].Framework {
				return rows[i].Concurrency < rows[j].Concurrency
			}
			return rows[i].Framework < rows[j].Framework
		})
		for _, r := range rows {
			fmt.Printf("%-10s %-6d %-12.0f %-12.2f\n", r.Framework, r.Concurrency, r.RPS, r.LatencyNs/1e6)
		}
	}
}

func writeCSV(results []Result) {
	f, err := os.Create("results.csv")
	if err != nil {
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"scenario", "framework", "concurrency", "rps", "p50_ms"})
	sorted := append([]Result(nil), results...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Scenario == sorted[j].Scenario {
			if sorted[i].Framework == sorted[j].Framework {
				return sorted[i].Concurrency < sorted[j].Concurrency
			}
			return sorted[i].Framework < sorted[j].Framework
		}
		return sorted[i].Scenario < sorted[j].Scenario
	})
	for _, r := range sorted {
		_ = w.Write([]string{
			string(r.Scenario),
			string(r.Framework),
			strconv.Itoa(r.Concurrency),
			fmt.Sprintf("%.0f", r.RPS),
			fmt.Sprintf("%.2f", r.LatencyNs/1e6),
		})
	}
}

func writeMarkdown(results []Result) {
	f, err := os.Create("results.md")
	if err != nil {
		return
	}
	defer f.Close()
	for _, sc := range []Scenario{ScenarioSimple, ScenarioJSON, ScenarioParams} {
		_, _ = fmt.Fprintf(f, "\n### Scenario: %s\n\n", sc)
		_, _ = fmt.Fprintln(f, "| Framework | Concurrency | RPS | p50 (ms) |")
		_, _ = fmt.Fprintln(f, "|-----------|-------------:|----:|---------:|")
		var rows []Result
		for _, r := range results {
			if r.Scenario == sc {
				rows = append(rows, r)
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Framework == rows[j].Framework {
				return rows[i].Concurrency < rows[j].Concurrency
			}
			return rows[i].Framework < rows[j].Framework
		})
		for _, r := range rows {
			_, _ = fmt.Fprintf(f, "| %s | %d | %.0f | %.2f |\n", r.Framework, r.Concurrency, r.RPS, r.LatencyNs/1e6)
		}
	}
}

func shouldProfile(spec string, fw Framework, sc Scenario, conc int) bool {
	if spec == "" {
		return false
	}
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return false
	}
	matchFW := parts[0] == "*" || parts[0] == string(fw)
	matchSc := parts[1] == "*" || parts[1] == string(sc)
	matchC := false
	if parts[2] == "*" {
		matchC = true
	} else {
		if n, err := strconv.Atoi(parts[2]); err == nil {
			matchC = (n == conc)
		}
	}
	return matchFW && matchSc && matchC
}
