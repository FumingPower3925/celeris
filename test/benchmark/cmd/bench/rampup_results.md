# Ramp-Up Benchmark Results

**Test Methodology**: Gradually increase concurrent clients (1 client every 100ms) until p95 latency exceeds 100ms or timeout.


## Scenario: simple

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 560 | 160633 | 3.37 | 14.0 |
| nethttp | 320 | 95734 | 2.43 | 8.0 |
| iris | 319 | 87093 | 2.73 | 8.0 |
| chi | 358 | 87035 | 3.51 | 9.0 |
| gin | 280 | 81674 | 2.26 | 7.0 |
| echo | 320 | 77583 | 3.16 | 8.0 |

## Scenario: json

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 600 | 145956 | 4.70 | 15.0 |
| nethttp | 280 | 84382 | 2.10 | 7.0 |
| echo | 280 | 75328 | 2.72 | 7.0 |
| gin | 239 | 74894 | 2.04 | 6.0 |
| chi | 200 | 74341 | 1.34 | 5.0 |
| iris | 240 | 70843 | 2.37 | 6.0 |

## Scenario: params

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 674 | 142688 | 6.15 | 17.0 |
| chi | 280 | 87707 | 2.31 | 7.0 |
| nethttp | 240 | 80135 | 1.63 | 6.0 |
| gin | 240 | 76982 | 1.88 | 6.0 |
| iris | 320 | 74215 | 3.52 | 8.0 |
| echo | 320 | 73488 | 3.55 | 8.0 |
