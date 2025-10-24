# Celeris Benchmarks

This directory contains benchmarks for both HTTP/1.1 and HTTP/2 implementations.

## Structure

```
test/benchmark/
├── http1/          # HTTP/1.1 ramp-up benchmarks
│   ├── main.go     # Benchmark runner comparing Celeris vs other frameworks
│   ├── go.mod      # Dependencies
│   └── results/    # Output: CSV, JSON, MD reports
├── http2/          # HTTP/2 ramp-up benchmarks  
│   ├── main.go     # Benchmark runner with h2c support
│   ├── go.mod      # Dependencies
│   └── results/    # Output: CSV, JSON, MD reports
└── README.md       # This file
```

## Running Benchmarks

### HTTP/1.1 Benchmarks
```bash
make test-rampup-h1
```

Tests Celeris HTTP/1.1 against:
- net/http (stdlib)
- Gin
- Echo
- Chi
- Fiber

### HTTP/2 Benchmarks
```bash
make test-rampup-h2
```

Tests Celeris HTTP/2 against:
- net/http with h2c
- Gin with h2c
- Echo with h2c
- Chi with h2c
- Iris with h2c

## Test Scenarios

Each framework is tested with three scenarios:

1. **simple**: Plain text response (`/`)
2. **json**: JSON response (`/json`)
3. **params**: Parameterized route with JSON (`/user/:userId/post/:postId`)

## Methodology

- **Ramp-Up Test**: Gradually increases concurrent clients until p95 latency exceeds 100ms
- **Client Addition Rate**: 1 client every 25ms (40 clients/second)
- **Measurement Window**: 1 second
- **Degradation Threshold**: p95 > 100ms
- **Max Test Duration**: 30 seconds

## Metrics

- **MaxClients**: Peak concurrent clients before degradation
- **MaxRPS**: Maximum requests/second achieved
- **P95AtMax**: 95th percentile latency (ms) at maximum RPS
- **TimeToDegrade**: Time (seconds) until performance degraded

## Output

Results are saved in three formats:
- **JSON**: `rampup_results_h1.json` / `rampup_results.json`
- **CSV**: `rampup_results_h1.csv` / `rampup_results.csv`
- **Markdown**: `rampup_results_h1.md` / `rampup_results.md`

## Notes

- Logs are silenced during benchmarks for clean output
- HTTP/1.1 uses standard TCP
- HTTP/2 uses h2c (cleartext) for fair comparison without TLS overhead
- Celeris automatically detects protocol and routes to appropriate handler
