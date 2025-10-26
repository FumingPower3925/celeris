# HTTP/1.1 Ramp-Up Benchmark Results

| Framework | Scenario | MaxClients | MaxRPS | P95(ms) | TimeToDegrade(s) |
|-----------|----------|------------|--------|---------|------------------|
| celeris | simple | 1120 | 154956 | 5.70 | 30.0 |
| celeris | json | 1040 | 157709 | 7.40 | 30.0 |
| celeris | params | 1000 | 152522 | 7.56 | 30.0 |
| nethttp | simple | 640 | 171996 | 1.99 | 30.0 |
| nethttp | json | 560 | 168974 | 1.70 | 30.0 |
| nethttp | params | 599 | 168890 | 1.87 | 30.0 |
| gin | simple | 600 | 171677 | 1.80 | 30.0 |
| gin | json | 678 | 165612 | 2.21 | 30.0 |
| gin | params | 600 | 153553 | 2.24 | 30.0 |
| echo | simple | 599 | 156527 | 2.68 | 30.0 |
| echo | json | 640 | 156580 | 2.23 | 30.0 |
| echo | params | 560 | 166655 | 1.52 | 30.0 |
| chi | simple | 560 | 168034 | 1.60 | 30.0 |
| chi | json | 560 | 144210 | 2.22 | 30.0 |
| chi | params | 596 | 148185 | 2.37 | 30.0 |
| fiber | simple | 600 | 170451 | 1.79 | 30.0 |
| fiber | json | 600 | 190155 | 1.49 | 30.0 |
| fiber | params | 640 | 189640 | 1.69 | 30.0 |
