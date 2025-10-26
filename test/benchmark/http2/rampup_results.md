# Ramp-Up Benchmark Results

**Test Methodology**: Gradually increase concurrent clients (1 client every 100ms) until p95 latency exceeds 100ms or timeout.


## Scenario: simple

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 885 | 158096 | 5.93 | 23.0 |
| nethttp | 400 | 99778 | 2.94 | 10.0 |
| iris | 280 | 89371 | 2.11 | 7.0 |
| chi | 240 | 86231 | 1.69 | 6.0 |
| echo | 320 | 73975 | 3.33 | 8.0 |
| gin | 280 | 73794 | 2.59 | 7.0 |

## Scenario: json

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 1071 | 156080 | 8.16 | 30.0 |
| nethttp | 280 | 75642 | 2.37 | 7.0 |
| iris | 240 | 74401 | 2.17 | 6.0 |
| echo | 240 | 72981 | 2.13 | 6.0 |
| chi | 320 | 72125 | 3.67 | 8.0 |
| gin | 240 | 71281 | 2.13 | 6.0 |

## Scenario: params

| Framework | Max Clients | Max RPS | P95 @ Max (ms) | Time to Degrade (s) |
|-----------|------------:|--------:|---------------:|--------------------:|
| celeris | 783 | 144727 | 5.91 | 20.0 |
| chi | 400 | 87216 | 3.45 | 10.0 |
| nethttp | 280 | 75103 | 2.45 | 7.0 |
| echo | 240 | 72466 | 2.24 | 6.0 |
| iris | 280 | 71496 | 2.99 | 7.0 |
| gin | 240 | 67208 | 2.36 | 6.0 |
