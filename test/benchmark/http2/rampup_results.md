# Ramp-Up Benchmark Results

**Test Methodology**: Gradually increase concurrent clients (1 client every 100ms) until p95 latency exceeds 100ms or timeout.


## Scenario: simple

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 360 | 97728 | 3.97 | 9.0 |

## Scenario: json

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 200 | 64134 | 1.28 | 5.0 |

## Scenario: params

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 200 | 66092 | 1.25 | 5.0 |
