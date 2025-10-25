# HTTP/1.1 Ramp-Up Benchmark Results

| Framework | Scenario | MaxClients | MaxRPS | P95(ms) | TimeToDegrade(s) |
|-----------|----------|------------|--------|---------|------------------|
| celeris | simple | 760 | 132440 | 5.89 | 30.0 |
| celeris | json | 0 | 0 | 0.00 | 30.0 |
| celeris | params | 1040 | 130382 | 9.75 | 30.0 |
| nethttp | simple | 480 | 171391 | 1.29 | 30.0 |
| nethttp | json | 520 | 168192 | 1.60 | 30.0 |
| nethttp | params | 480 | 164408 | 1.33 | 30.0 |
| gin | simple | 559 | 168842 | 1.76 | 30.0 |
| gin | json | 400 | 145744 | 1.02 | 30.0 |
| gin | params | 400 | 158730 | 0.66 | 30.0 |
| echo | simple | 480 | 145710 | 1.82 | 30.0 |
| echo | json | 440 | 164279 | 0.92 | 30.0 |
| echo | params | 440 | 144041 | 1.57 | 30.0 |
| chi | simple | 520 | 175048 | 1.40 | 30.0 |
| chi | json | 440 | 166547 | 0.82 | 30.0 |
| chi | params | 400 | 157544 | 0.58 | 30.0 |
| fiber | simple | 440 | 169486 | 0.61 | 30.0 |
| fiber | json | 560 | 193553 | 1.32 | 30.0 |
| fiber | params | 520 | 194295 | 0.85 | 30.0 |
