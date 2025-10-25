# HTTP/1.1 Ramp-Up Benchmark Results

| Framework | Scenario | MaxClients | MaxRPS | P95(ms) | TimeToDegrade(s) |
|-----------|----------|------------|--------|---------|------------------|
| celeris | simple | 0 | 0 | 0.00 | 30.0 |
| celeris | json | 800 | 126930 | 6.37 | 30.0 |
| celeris | params | 0 | 0 | 0.00 | 30.0 |
| nethttp | simple | 520 | 167330 | 1.86 | 30.0 |
| nethttp | json | 480 | 160730 | 1.45 | 30.0 |
| nethttp | params | 400 | 152565 | 0.94 | 30.0 |
| gin | simple | 520 | 172659 | 1.53 | 30.0 |
| gin | json | 440 | 142918 | 1.62 | 30.0 |
| gin | params | 440 | 168538 | 0.91 | 30.0 |
| echo | simple | 520 | 172491 | 1.41 | 30.0 |
| echo | json | 400 | 159841 | 0.58 | 30.0 |
| echo | params | 480 | 169713 | 1.05 | 30.0 |
| chi | simple | 440 | 161529 | 0.92 | 30.0 |
| chi | json | 400 | 153702 | 0.63 | 30.0 |
| chi | params | 400 | 162019 | 0.59 | 30.0 |
| fiber | simple | 440 | 170756 | 0.67 | 30.0 |
| fiber | json | 560 | 193230 | 1.39 | 30.0 |
| fiber | params | 560 | 191769 | 1.25 | 30.0 |
