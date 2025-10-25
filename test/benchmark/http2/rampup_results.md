# Ramp-Up Benchmark Results

**Test Methodology**: Gradually increase concurrent clients (1 client every 100ms) until p95 latency exceeds 100ms or timeout.


## Scenario: simple

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| nethttp | 360 | 97807 | 2.40 | 9.0 |
| iris | 320 | 87445 | 2.75 | 8.0 |
| chi | 360 | 85465 | 3.23 | 9.0 |
| gin | 320 | 72866 | 3.44 | 8.0 |
| echo | 240 | 70696 | 2.17 | 6.0 |
| celeris | 39 | 100 | 0.28 | 1.0 |

## Scenario: json

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| nethttp | 320 | 79385 | 2.93 | 8.0 |
| chi | 320 | 74155 | 3.44 | 8.0 |
| iris | 320 | 71775 | 3.65 | 8.0 |
| echo | 240 | 71097 | 2.19 | 6.0 |
| gin | 240 | 69600 | 2.21 | 6.0 |
| celeris | 40 | 100 | 1.67 | 1.0 |

## Scenario: params

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| chi | 360 | 83952 | 3.27 | 9.0 |
| nethttp | 240 | 73233 | 1.99 | 6.0 |
| iris | 200 | 70092 | 1.57 | 5.0 |
| echo | 240 | 68386 | 2.35 | 6.0 |
| gin | 280 | 68308 | 3.05 | 7.0 |
| celeris | 39 | 100 | 0.20 | 1.0 |
