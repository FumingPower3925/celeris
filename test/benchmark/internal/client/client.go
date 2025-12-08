package client

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// RoundRobinTransport implements http.RoundTripper and dispatches requests across
// multiple underlying http.RoundTrippers.
type RoundRobinTransport struct {
	Transports []http.RoundTripper
	idx        uint64
}

func (r *RoundRobinTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(r.Transports) == 0 {
		return http.DefaultTransport.RoundTrip(req)
	}
	i := atomic.AddUint64(&r.idx, 1)
	t := r.Transports[int(i)%len(r.Transports)]
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

type Measurement struct {
	Timestamp time.Time
	LatencyNs float64
	Success   bool
}

const (
	RampUpInterval    = 25 * time.Millisecond
	MeasureWindow     = 1 * time.Second
	DegradationThresh = 100.0 // ms
	TimeoutThresh     = 3 * time.Second
	MaxTestDuration   = 30 * time.Second
	RequestDelay      = 0 * time.Millisecond
)

// WorkerFunc is the function that simulates a single client.
type WorkerFunc func(stopCh <-chan struct{}, measurements chan<- Measurement, wg *sync.WaitGroup)

// RunRampUp performs the ramp-up test using the provided worker factory.
func RunRampUp(url, fw, sc string, createWorker func() WorkerFunc) RampUpResult {
	var mu sync.Mutex
	var measurements []Measurement
	var activeClients int32
	stopChan := make(chan struct{})
	var wg sync.WaitGroup
	measurementsChan := make(chan Measurement, 10000)

	startTime := time.Now()
	maxClients := 0
	maxRPS := 0.0
	p95AtMax := 0.0
	degradeTime := 0.0

	// Collector goroutine
	go func() {
		for m := range measurementsChan {
			mu.Lock()
			measurements = append(measurements, m)
			mu.Unlock()
		}
	}()

	addClientTicker := time.NewTicker(RampUpInterval)
	defer addClientTicker.Stop()

	measureTicker := time.NewTicker(MeasureWindow)
	defer measureTicker.Stop()

	testLoop := true
	emptyWindows := 0

	for testLoop {
		select {
		case <-addClientTicker.C:
			wg.Add(1)
			atomic.AddInt32(&activeClients, 1)
			worker := createWorker()
			go func() {
				defer wg.Done()
				defer atomic.AddInt32(&activeClients, -1)
				worker(stopChan, measurementsChan, nil) // wg handled by wrapper
			}()

		case <-measureTicker.C:
			mu.Lock()
			now := time.Now()
			cutoff := now.Add(-MeasureWindow)
			var recent []float64
			successCount := 0

			// Optimize: only look at recent measurements
			// Since we append, we can search from end
			startIdx := len(measurements)
			for i := len(measurements) - 1; i >= 0; i-- {
				if measurements[i].Timestamp.Before(cutoff) {
					startIdx = i + 1
					break
				}
				startIdx = i
			}

			for i := startIdx; i < len(measurements); i++ {
				if measurements[i].Success {
					recent = append(recent, measurements[i].LatencyNs)
					successCount++
				}
			}

			// Prune old measurements to save memory if list gets too big
			if len(measurements) > 100000 {
				measurements = measurements[startIdx:]
			}
			mu.Unlock()

			clients := int(atomic.LoadInt32(&activeClients))

			if len(recent) == 0 {
				emptyWindows++
				if clients > 20 && emptyWindows >= 3 {
					fmt.Printf("  ✗ FAILED: No successful requests with %d clients\n", clients)
					testLoop = false
				}
				continue
			} else {
				emptyWindows = 0
			}

			p95 := Percentile(recent, 95)
			rps := float64(successCount) / MeasureWindow.Seconds()

			if rps > maxRPS {
				maxRPS = rps
				maxClients = clients
				p95AtMax = p95 / 1e6
				degradeTime = time.Since(startTime).Seconds()
			}

			if p95/1e6 > DegradationThresh {
				fmt.Printf("  ⚠ Degraded: p95=%.2fms\n", p95/1e6)
				testLoop = false
			}

			if time.Since(startTime) > MaxTestDuration {
				testLoop = false
			}
		}
	}

	close(stopChan)
	// Wait for workers to stop
	// Note: workers might be stuck in IO, so we rely on timeouts in workers
	// We give them a bit of time
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		close(measurementsChan)
	case <-time.After(2 * time.Second):
		// Force exit, do not close measurementsChan to avoid panic in stuck workers
		fmt.Println("  ⚠ Timeout waiting for workers to stop")
	}

	return RampUpResult{
		Framework:     fw,
		Scenario:      sc,
		MaxClients:    maxClients,
		MaxRPS:        maxRPS,
		P95AtMax:      p95AtMax,
		TimeToDegrade: degradeTime,
	}
}

func Percentile(vals []float64, p float64) float64 {
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

func PrintResults(results []RampUpResult, title string) {
	fmt.Printf("\n\n=== %s RESULTS ===\n\n", title)
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

func SaveResults(results []RampUpResult, name string) {
	f, _ := os.Create(name + "_results.json")
	defer f.Close()
	_ = json.NewEncoder(f).Encode(results)

	f2, _ := os.Create(name + "_results.csv")
	defer f2.Close()
	w := csv.NewWriter(f2)
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

// StandardHTTPWorker returns a worker that uses a standard http.Client
func StandardHTTPWorker(client *http.Client, url string) WorkerFunc {
	return func(stopCh <-chan struct{}, measurements chan<- Measurement, _ *sync.WaitGroup) {
		for {
			select {
			case <-stopCh:
				return
			default:
			}

			reqStart := time.Now()
			req, _ := http.NewRequest("GET", url, nil)
			resp, err := client.Do(req)
			latency := time.Since(reqStart)

			success := err == nil && resp != nil && resp.StatusCode == 200
			if success {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			} else if resp != nil {
				_ = resp.Body.Close()
			}

			select {
			case measurements <- Measurement{
				Timestamp: reqStart,
				LatencyNs: float64(latency.Nanoseconds()),
				Success:   success,
			}:
			case <-stopCh:
				return
			}

			time.Sleep(RequestDelay)
		}
	}
}

// PipelinedHTTP1Worker returns a worker that uses HTTP/1.1 pipelining with proper response parsing
func PipelinedHTTP1Worker(addr, path, host string, batchSize int) WorkerFunc {
	return func(stopCh <-chan struct{}, measurements chan<- Measurement, _ *sync.WaitGroup) {
		// Helper to establish connection with buffered reader
		type connState struct {
			conn   net.Conn
			reader *bufio.Reader
		}

		connect := func() *connState {
			conn, err := net.DialTimeout("tcp", addr, TimeoutThresh)
			if err != nil {
				return nil
			}
			return &connState{
				conn:   conn,
				reader: bufio.NewReader(conn),
			}
		}

		state := connect()
		if state == nil {
			return
		}
		defer func() {
			if state != nil && state.conn != nil {
				state.conn.Close()
			}
		}()

		// Pre-build request with Connection: keep-alive for pipelining
		reqBuf := []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n", path, host))

		for {
			select {
			case <-stopCh:
				return
			default:
			}

			batchStart := time.Now()

			// Set deadline for the whole batch
			_ = state.conn.SetDeadline(time.Now().Add(TimeoutThresh))

			// Send all requests in the batch (pipelining)
			sendFailed := false
			for i := 0; i < batchSize; i++ {
				_, err := state.conn.Write(reqBuf)
				if err != nil {
					sendFailed = true
					break
				}
			}

			if sendFailed {
				// Reconnect
				state.conn.Close()
				state = connect()
				if state == nil {
					return
				}
				continue
			}

			// Read responses using proper HTTP parsing
			successCount := 0
			for i := 0; i < batchSize; i++ {
				resp, err := http.ReadResponse(state.reader, nil)
				if err != nil {
					// Reconnect on error
					state.conn.Close()
					state = connect()
					if state == nil {
						return
					}
					break
				}
				if resp.StatusCode == 200 {
					successCount++
				}
				// Drain and close the body to allow connection reuse
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			latency := time.Since(batchStart)

			// Report average latency per request in batch
			avgLatency := float64(latency.Nanoseconds()) / float64(batchSize)

			for i := 0; i < successCount; i++ {
				select {
				case measurements <- Measurement{
					Timestamp: batchStart,
					LatencyNs: avgLatency,
					Success:   true,
				}:
				case <-stopCh:
					return
				}
			}

			time.Sleep(RequestDelay)
		}
	}
}

// BatchHTTP2Worker returns a worker that sends concurrent requests on H2
func BatchHTTP2Worker(client *http.Client, url string, batchSize int) WorkerFunc {
	return func(stopCh <-chan struct{}, measurements chan<- Measurement, _ *sync.WaitGroup) {
		for {
			select {
			case <-stopCh:
				return
			default:
			}

			var wg sync.WaitGroup
			wg.Add(batchSize)

			for i := 0; i < batchSize; i++ {
				go func() {
					defer wg.Done()
					reqStart := time.Now()
					req, _ := http.NewRequest("GET", url, nil)
					resp, err := client.Do(req)
					latency := time.Since(reqStart)

					success := err == nil && resp != nil && resp.StatusCode == 200
					if success {
						_, _ = io.Copy(io.Discard, resp.Body)
						_ = resp.Body.Close()
					} else if resp != nil {
						_ = resp.Body.Close()
					}

					select {
					case measurements <- Measurement{
						Timestamp: reqStart,
						LatencyNs: float64(latency.Nanoseconds()),
						Success:   success,
					}:
					case <-stopCh:
						return
					}
				}()
			}

			wg.Wait()
			time.Sleep(RequestDelay)
		}
	}
}
