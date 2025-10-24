# HTTP/1.1 Ramp-Up Benchmark Results

| Framework | Scenario | MaxClients | MaxRPS | P95(ms) | TimeToDegrade(s) |
|-----------|----------|------------|--------|---------|------------------|
| celeris | simple | 760 | 130024 | 5.84 | 30.0 |
| celeris | json | 0 | 0 | 0.00 | 30.0 |
| celeris | params | 760 | 127524 | 5.74 | 30.0 |
| nethttp | simple | 480 | 153180 | 2.09 | 30.0 |
| nethttp | json | 480 | 155614 | 1.93 | 30.0 |
| nethttp | params | 360 | 131095 | 1.14 | 30.0 |
| gin | simple | 320 | 124282 | 0.83 | 30.0 |
| gin | json | 359 | 142744 | 0.75 | 30.0 |
| gin | params | 320 | 127621 | 0.55 | 30.0 |
| echo | simple | 440 | 158786 | 1.31 | 30.0 |
| echo | json | 358 | 143408 | 0.58 | 30.0 |
| echo | params | 316 | 125926 | 0.50 | 30.0 |
| chi | simple | 440 | 164414 | 1.07 | 30.0 |
| chi | json | 479 | 153723 | 1.56 | 30.0 |
| chi | params | 1145 | 114111 | 14.39 | 30.0 |
| fiber | simple | 400 | 163771 | 0.59 | 30.0 |
| fiber | json | 480 | 178110 | 1.14 | 30.0 |
| fiber | params | 560 | 172466 | 1.85 | 30.0 |
