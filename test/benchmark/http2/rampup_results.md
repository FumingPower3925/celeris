# Ramp-Up Benchmark Results

**Test Methodology**: Gradually increase concurrent clients (1 client every 100ms) until p95 latency exceeds 100ms or timeout.


## Scenario: simple

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 280 | 97857 | 1.56 | 7.0 |

## Scenario: json

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 240 | 77862 | 2.06 | 6.0 |

## Scenario: params

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 240 | 85325 | 1.05 | 6.0 |
