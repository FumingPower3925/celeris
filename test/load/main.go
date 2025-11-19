// Package main provides incremental load testing for Celeris HTTP server reliability.
// It tests connection handling behavior with gradual load increase to detect
// connection drops and 503 responses under increasing load.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
	"golang.org/x/net/http2"
)

// IncrementalLoadTestConfig defines the configuration for incremental load tests
type IncrementalLoadTestConfig struct {
	// Server configuration
	ServerAddr           string
	EnableH1             bool
	EnableH2             bool
	MaxConcurrentStreams uint32
	MaxConnections       uint32

	// Protocol flags
	HTTP1Only bool
	HTTP2Only bool
	Mixed     bool

	// Test configuration - matching rampup benchmark
	RampUpInterval time.Duration // Time between adding new clients (25ms like rampup)
	ClientsPerStep int           // Number of clients to add each step (1 like rampup)
	TestDuration   time.Duration // Total test duration (30s like rampup)
	RequestTimeout time.Duration // Request timeout (3s like rampup)
	RequestDelay   time.Duration // Delay between requests per client (2ms like rampup)
}

// IncrementalLoadTestResult contains the results of an incremental load test
type IncrementalLoadTestResult struct {
	Protocol           string
	TestDuration       time.Duration
	TotalSteps         int
	MaxClients         int
	TotalRequests      int64
	SuccessfulRequests int64
	FailedRequests     int64
	DroppedConnections int64
	StatusCodes        map[int]int64
	Steps              []StepResult
	MaxRPS             float64
	MaxClientsAtMaxRPS int
}

// StepResult contains the results for a single step
type StepResult struct {
	StepNumber        int
	ClientCount       int
	TimeElapsed       time.Duration
	Requests          int64
	Successful        int64
	Failed            int64
	Dropped           int64
	StatusCodes       map[int]int64
	RequestsPerSecond float64
	SuccessRate       float64
	DropRate          float64
}

// IncrementalLoadTestRunner manages the incremental load test
type IncrementalLoadTestRunner struct {
	config      IncrementalLoadTestConfig
	server      *celeris.Server
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.Mutex
	result      *IncrementalLoadTestResult
	stepResults []StepResult
	startTime   time.Time
	clients     []*http.Client
}

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
	//nolint:gosec // i is bounded by number of transports, int conversion is safe
	t := r.transports[int(i)%len(r.transports)]
	return t.RoundTrip(req)
}

// NewIncrementalLoadTestRunner creates a new incremental load test runner
func NewIncrementalLoadTestRunner(config IncrementalLoadTestConfig) *IncrementalLoadTestRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &IncrementalLoadTestRunner{
		config: config,
		ctx:    ctx,
		cancel: cancel,
		result: &IncrementalLoadTestResult{
			StatusCodes: make(map[int]int64),
			Steps:       make([]StepResult, 0),
		},
		stepResults: make([]StepResult, 0),
		clients:     make([]*http.Client, 0),
	}
}

// StartServer starts the Celeris server
func (ltr *IncrementalLoadTestRunner) StartServer() {
	// Create server configuration
	serverConfig := celeris.Config{
		Addr:                 ltr.config.ServerAddr,
		EnableH1:             ltr.config.EnableH1,
		EnableH2:             ltr.config.EnableH2,
		MaxConcurrentStreams: ltr.config.MaxConcurrentStreams,
		MaxConnections:       ltr.config.MaxConnections,
		Multicore:            true,
		NumEventLoop:         -1, // Use all available cores
	}

	// Server configuration loaded

	// Create and start server
	ltr.server = celeris.New(serverConfig).Handler(celeris.HandlerFunc(func(ctx *celeris.Context) error {
		return ctx.String(200, "OK")
	}))

	// Start server in a goroutine
	go func() {
		_ = ltr.server.Start() // Ignore errors for silent operation
	}()

	// Wait a moment for server to start
	time.Sleep(100 * time.Millisecond)
}

// StopServer stops the Celeris server
func (ltr *IncrementalLoadTestRunner) StopServer() error {
	if ltr.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return ltr.server.Stop(ctx)
	}
	return nil
}

// createOptimizedClient creates an optimized HTTP client like the rampup benchmark
func (ltr *IncrementalLoadTestRunner) createOptimizedClient(useHTTP2 bool) *http.Client {
	if useHTTP2 {
		// HTTP/2 client with single transport
		return &http.Client{
			Timeout: ltr.config.RequestTimeout,
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
					//nolint:noctx // DialTLS interface doesn't provide context, use dialer with timeout
					dialer := &net.Dialer{Timeout: 10 * time.Second}
					return dialer.Dial(network, addr)
				},
			},
		}
	}
	{
		// HTTP/1.1 client with multiple transports for load balancing
		base := &http.Transport{
			MaxIdleConns:        10000,
			MaxIdleConnsPerHost: 10000,
			MaxConnsPerHost:     10000,
			DisableCompression:  true,
			IdleConnTimeout:     90 * time.Second,
		}

		numClients := 4
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

		return &http.Client{
			Timeout:   ltr.config.RequestTimeout,
			Transport: &rrTransport{transports: trs},
		}
	}
}

// RunIncrementalLoadTest executes the incremental load test
func (ltr *IncrementalLoadTestRunner) RunIncrementalLoadTest() (*IncrementalLoadTestResult, error) {
	startTime := time.Now()
	ltr.startTime = startTime

	// Start server
	ltr.StartServer()
	defer func() {
		_ = ltr.StopServer() // Silent shutdown
	}()

	// Wait for server to be ready and verify it's listening
	time.Sleep(3 * time.Second)

	// Health check to verify server is listening
	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://"+ltr.config.ServerAddr+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Determine protocol to test
	protocol := "mixed"
	if ltr.config.HTTP1Only {
		protocol = "HTTP/1.1"
	} else if ltr.config.HTTP2Only {
		protocol = "HTTP/2"
	}

	ltr.result.Protocol = protocol

	// Calculate total steps (like rampup: 25ms intervals for 30s = 1200 steps)
	totalSteps := int(ltr.config.TestDuration / ltr.config.RampUpInterval)
	ltr.result.TotalSteps = totalSteps
	ltr.result.MaxClients = totalSteps * ltr.config.ClientsPerStep

	// Start incremental load test
	go ltr.runIncrementalLoad()

	// Wait for test to complete
	time.Sleep(ltr.config.TestDuration + 2*time.Second)

	// Stop all clients
	ltr.cancel()
	ltr.wg.Wait()

	// Calculate final results
	ltr.result.TestDuration = time.Since(startTime)
	ltr.result.Steps = ltr.stepResults

	return ltr.result, nil
}

// runIncrementalLoad runs the incremental load test
func (ltr *IncrementalLoadTestRunner) runIncrementalLoad() {
	stepNumber := 0
	ticker := time.NewTicker(ltr.config.RampUpInterval)
	defer ticker.Stop()

	// Measurement ticker (1 second intervals like rampup)
	measureTicker := time.NewTicker(1 * time.Second)
	defer measureTicker.Stop()

	// Track recent measurements for RPS calculation
	var recentMeasurements []measurement
	var measurementsMu sync.Mutex

	// Start measurement goroutine (like rampup benchmark)
	go func() {
		lastTime := time.Now()

		for {
			select {
			case <-ltr.ctx.Done():
				return
			case <-measureTicker.C:
				measurementsMu.Lock()
				now := time.Now()
				cutoff := now.Add(-1 * time.Second) // Last 1 second

				// Count recent successful requests (like rampup benchmark)
				successCount := 0
				for i := len(recentMeasurements) - 1; i >= 0; i-- {
					if recentMeasurements[i].timestamp.Before(cutoff) {
						break
					}
					if recentMeasurements[i].success {
						successCount++
					}
				}

				// Calculate RPS for this window (like rampup benchmark)
				elapsed := now.Sub(lastTime).Seconds()
				if elapsed > 0 {
					rps := float64(successCount) / elapsed

					// Update max RPS
					if rps > ltr.result.MaxRPS {
						ltr.result.MaxRPS = rps
						ltr.result.MaxClientsAtMaxRPS = len(ltr.clients)
					}
				}

				lastTime = now
				measurementsMu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ltr.ctx.Done():
			return
		case <-ticker.C:
			stepNumber++
			clientCount := stepNumber * ltr.config.ClientsPerStep

			// Start clients for this step
			ltr.startClientsForStep(stepNumber, clientCount, &recentMeasurements, &measurementsMu)

			// Record step results after a short delay to let requests complete
			time.Sleep(50 * time.Millisecond)
			ltr.recordStepResults(0, clientCount) // Always record step 0 (all requests)

			// Stop adding new clients if we've reached the test duration
			if time.Since(ltr.startTime) >= ltr.config.TestDuration {
				return
			}
		}
	}
}

// measurement tracks a single request measurement
type measurement struct {
	timestamp time.Time
	success   bool
}

// startClientsForStep starts clients for a specific step
func (ltr *IncrementalLoadTestRunner) startClientsForStep(_ int, clientCount int, recentMeasurements *[]measurement, measurementsMu *sync.Mutex) {
	for i := 0; i < ltr.config.ClientsPerStep; i++ {
		// Determine which protocol to use for this client
		useHTTP2 := false
		if ltr.config.HTTP2Only {
			useHTTP2 = true
		} else if ltr.config.Mixed {
			// For mixed tests, alternate between HTTP/1.1 and HTTP/2
			useHTTP2 = (clientCount % 2) == 0
		}

		// Create optimized client
		client := ltr.createOptimizedClient(useHTTP2)
		ltr.clients = append(ltr.clients, client)

		ltr.wg.Add(1)
		go ltr.runClient(0, clientCount, client, useHTTP2, recentMeasurements, measurementsMu) // Use step 0 for all requests
	}
}

// runClient runs a single client (matching rampup benchmark behavior)
func (ltr *IncrementalLoadTestRunner) runClient(stepNumber int, _ int, client *http.Client, useHTTP2 bool, recentMeasurements *[]measurement, measurementsMu *sync.Mutex) {
	defer ltr.wg.Done()

	// Pre-create request for better performance
	url := "http://" + ltr.config.ServerAddr + "/"
	req, _ := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if useHTTP2 {
		req.Header.Set("User-Agent", "HTTP/2-Client")
	} else {
		req.Header.Set("User-Agent", "HTTP/1.1-Client")
	}

	// Make requests at 500 RPS per client (like rampup benchmark: 2ms delay)
	requestCount := 0
	for {
		// Check if test should stop
		select {
		case <-ltr.ctx.Done():
			return
		default:
		}

		start := time.Now()
		resp, err := client.Do(req)
		_ = time.Since(start) // latency not used in this implementation

		// Track errors silently (no debug output)

		// Track the request (like rampup: only count successful requests for RPS)
		success := err == nil && resp != nil && resp.StatusCode == 200

		// Record measurement for RPS calculation
		measurementsMu.Lock()
		*recentMeasurements = append(*recentMeasurements, measurement{
			timestamp: start,
			success:   success,
		})
		// Keep only last 1000 measurements to avoid memory growth
		if len(*recentMeasurements) > 1000 {
			*recentMeasurements = (*recentMeasurements)[len(*recentMeasurements)-1000:]
		}
		measurementsMu.Unlock()

		// Track detailed results silently
		ltr.trackRequest(stepNumber, resp, err, success)

		// Close response body
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		requestCount++

		// 2ms delay between requests (like rampup benchmark: 500 RPS per client)
		time.Sleep(2 * time.Millisecond)
	}
}

// trackRequest tracks a single request
func (ltr *IncrementalLoadTestRunner) trackRequest(stepNumber int, resp *http.Response, err error, success bool) {
	ltr.mu.Lock()
	defer ltr.mu.Unlock()

	// Initialize step result if it doesn't exist
	if stepNumber >= len(ltr.stepResults) {
		// Fill any missing steps (including step 0)
		for i := len(ltr.stepResults); i <= stepNumber; i++ {
			ltr.stepResults = append(ltr.stepResults, StepResult{
				StepNumber:  i,
				StatusCodes: make(map[int]int64),
			})
		}
	}

	// Ensure we have the current step
	if stepNumber >= 0 && stepNumber < len(ltr.stepResults) {
		step := &ltr.stepResults[stepNumber]
		step.Requests++

		if err != nil {
			step.Failed++
			// Track as dropped connection (000 status code)
			step.StatusCodes[0]++
		} else {
			statusCode := resp.StatusCode
			step.StatusCodes[statusCode]++

			if success {
				step.Successful++
			} else {
				step.Failed++
			}
		}
	}
}

// recordStepResults records the results for a step
func (ltr *IncrementalLoadTestRunner) recordStepResults(stepNumber, clientCount int) {
	ltr.mu.Lock()
	defer ltr.mu.Unlock()

	if stepNumber >= 0 && stepNumber < len(ltr.stepResults) {
		step := &ltr.stepResults[stepNumber]
		step.StepNumber = stepNumber
		step.ClientCount = clientCount
		step.TimeElapsed = time.Since(ltr.startTime)

		// Calculate requests per second for this step
		if step.TimeElapsed.Seconds() > 0 {
			step.RequestsPerSecond = float64(step.Requests) / step.TimeElapsed.Seconds()
		}

		// Calculate success and drop rates
		if step.Requests > 0 {
			step.SuccessRate = float64(step.Successful) / float64(step.Requests) * 100
			step.DropRate = float64(step.Dropped) / float64(step.Requests) * 100
		}

		// Update total results
		ltr.result.TotalRequests += step.Requests
		ltr.result.SuccessfulRequests += step.Successful
		ltr.result.FailedRequests += step.Failed

		// Step progress logged silently

		// Count dropped connections (status code 0)
		if dropped, exists := step.StatusCodes[0]; exists {
			step.Dropped = dropped
			ltr.result.DroppedConnections += dropped
		}

		// Update total status codes
		for statusCode, count := range step.StatusCodes {
			ltr.result.StatusCodes[statusCode] += count
		}
	}
}

// PrintResults prints the summarized test results
func (ltr *IncrementalLoadTestRunner) PrintResults() {
	fmt.Printf("\n=== Incremental Load Test Results ===\n")
	fmt.Printf("Protocol: %s\n", ltr.result.Protocol)
	fmt.Printf("Test Duration: %v\n", ltr.result.TestDuration)
	fmt.Printf("Max Clients: %d\n", ltr.result.MaxClients)
	fmt.Printf("Max RPS: %.0f (at %d clients)\n", ltr.result.MaxRPS, ltr.result.MaxClientsAtMaxRPS)
	fmt.Printf("Total Requests: %d\n", ltr.result.TotalRequests)
	fmt.Printf("Successful Requests: %d\n", ltr.result.SuccessfulRequests)
	fmt.Printf("Failed Requests: %d\n", ltr.result.FailedRequests)
	fmt.Printf("Dropped Connections: %d\n", ltr.result.DroppedConnections)

	// Show key performance milestones
	fmt.Printf("\n=== Key Performance Milestones ===\n")
	milestones := []int{100, 500, 1000, 2000, 3000}
	for _, target := range milestones {
		if target > ltr.result.MaxClients {
			break
		}
		// Find the step with closest client count to target
		var bestStep *StepResult
		for i := range ltr.stepResults {
			step := &ltr.stepResults[i]
			if step.ClientCount >= target {
				if bestStep == nil || step.ClientCount < bestStep.ClientCount {
					bestStep = step
				}
			}
		}
		if bestStep != nil {
			status503 := bestStep.StatusCodes[503]
			fmt.Printf("  %d clients: %.1f RPS, %.1f%% success, %d 503s\n",
				bestStep.ClientCount, bestStep.RequestsPerSecond, bestStep.SuccessRate, status503)
		}
	}

	fmt.Printf("\n=== Status Code Distribution ===\n")
	for statusCode, count := range ltr.result.StatusCodes {
		percentage := float64(0)
		if ltr.result.TotalRequests > 0 {
			percentage = float64(count) / float64(ltr.result.TotalRequests) * 100
		}
		fmt.Printf("  %d: %d (%.2f%%)\n", statusCode, count, percentage)
	}

	// Test validation
	fmt.Printf("\n=== Test Validation ===\n")
	if ltr.result.DroppedConnections > 0 {
		fmt.Printf("❌ TEST FAILED: %d connections were dropped without proper HTTP response codes\n", ltr.result.DroppedConnections)
		fmt.Printf("This indicates that Celeris is closing connections inappropriately.\n")
		fmt.Printf("Expected behavior: Server should return proper HTTP error codes (503, 429, 400) instead of dropping connections.\n")
		os.Exit(1) // Fail the test immediately
	}
	fmt.Printf("✅ TEST PASSED: No connections were dropped without proper HTTP response codes\n")
	fmt.Printf("Celeris is handling connections correctly and returning proper HTTP responses.\n")

	// Check for 503 responses
	status503 := ltr.result.StatusCodes[503]
	if status503 > 0 {
		percentage := float64(0)
		if ltr.result.TotalRequests > 0 {
			percentage = float64(status503) / float64(ltr.result.TotalRequests) * 100
		}
		fmt.Printf("⚠️  WARNING: %d requests returned 503 Service Unavailable (%.2f%%)\n", status503, percentage)
		fmt.Printf("This may be expected behavior under high load (server at capacity).\n")
	}

	// Overall success rate
	overallSuccessRate := float64(0)
	if ltr.result.TotalRequests > 0 {
		overallSuccessRate = float64(ltr.result.SuccessfulRequests) / float64(ltr.result.TotalRequests) * 100
	}
	fmt.Printf("Overall Success Rate: %.2f%%\n", overallSuccessRate)
}

func main() {
	// Command line flags - matching rampup benchmark parameters
	var (
		serverAddr     = flag.String("addr", "localhost:8080", "Server address")
		maxConnections = flag.Uint("max-conn", 10000, "Server max connections")
		maxStreams     = flag.Uint("max-streams", 10000, "Server max concurrent streams")
		rampUpInterval = flag.Duration("rampup", 25*time.Millisecond, "Time between adding new clients (like rampup)")
		clientsPerStep = flag.Int("clients", 1, "Number of clients to add each step (like rampup)")
		testDuration   = flag.Duration("duration", 30*time.Second, "Test duration (like rampup)")
		requestTimeout = flag.Duration("timeout", 3*time.Second, "Request timeout (like rampup)")
		requestDelay   = flag.Duration("delay", 2*time.Millisecond, "Delay between requests per client (like rampup)")
		protocol       = flag.String("protocol", "mixed", "Protocol to test: http1, http2, or mixed")
		verbose        = flag.Bool("verbose", false, "Verbose output")
	)
	flag.Parse()

	// Create incremental load test configuration based on protocol
	config := IncrementalLoadTestConfig{
		ServerAddr:           *serverAddr,
		MaxConcurrentStreams: uint32(*maxStreams),     //nolint:gosec // safe: bounded by flag input
		MaxConnections:       uint32(*maxConnections), //nolint:gosec // safe: bounded by flag input
		RampUpInterval:       *rampUpInterval,
		ClientsPerStep:       *clientsPerStep,
		TestDuration:         *testDuration,
		RequestTimeout:       *requestTimeout,
		RequestDelay:         *requestDelay,
	}

	// Set protocol-specific configuration
	switch *protocol {
	case "http1":
		config.EnableH1 = true
		config.EnableH2 = false
		config.HTTP1Only = true
	case "http2":
		config.EnableH1 = false
		config.EnableH2 = true
		config.HTTP2Only = true
	case "mixed":
		config.EnableH1 = true
		config.EnableH2 = true
		config.Mixed = true
	default:
		log.Fatalf("Invalid protocol: %s. Must be http1, http2, or mixed", *protocol)
	}
	// Protocol configuration complete

	if *verbose {
		log.Println("Verbose mode enabled")
	}

	// Create and run the incremental load test
	runner := NewIncrementalLoadTestRunner(config)
	result, err := runner.RunIncrementalLoadTest()
	if err != nil {
		log.Fatalf("Load test failed: %v", err)
	}

	// Print results
	runner.PrintResults()

	// Exit with error code if test failed
	if result.DroppedConnections > 0 {
		os.Exit(1)
	}
}
