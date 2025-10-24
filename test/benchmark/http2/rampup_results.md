# Ramp-Up Benchmark Results

**Test Methodology**: Gradually increase concurrent clients (1 client every 100ms) until p95 latency exceeds 100ms or timeout.


## Scenario: simple

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| nethttp | 280 | 94967 | 1.61 | 7.0 |
| chi | 280 | 91457 | 2.12 | 7.0 |
| iris | 360 | 88133 | 3.20 | 9.0 |
| gin | 240 | 80796 | 1.77 | 6.0 |
| echo | 240 | 79852 | 1.91 | 6.0 |
| celeris | 559 | 20568 | 42.23 | 14.0 |

## Scenario: json

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| nethttp | 320 | 86838 | 2.58 | 8.0 |
| gin | 320 | 81038 | 2.99 | 8.0 |
| chi | 280 | 79557 | 2.68 | 7.0 |
| echo | 280 | 76541 | 2.76 | 7.0 |
| iris | 280 | 75296 | 2.89 | 7.0 |
| celeris | 440 | 19680 | 35.44 | 11.0 |

## Scenario: params

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| chi | 360 | 85605 | 3.57 | 9.0 |
| iris | 240 | 74326 | 2.22 | 6.0 |
| echo | 240 | 74120 | 2.19 | 6.0 |
| gin | 240 | 71134 | 2.51 | 6.0 |
| nethttp | 240 | 68196 | 2.77 | 6.0 |
| celeris | 440 | 20191 | 35.01 | 11.0 |
