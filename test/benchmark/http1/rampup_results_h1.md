# HTTP/1.1 Ramp-Up Benchmark Results

| Framework | Scenario | MaxClients | MaxRPS | P95(ms) | TimeToDegrade(s) |
|-----------|----------|------------|--------|---------|------------------|
| celeris | simple | 840 | 75 | 1976.73 | 23.0 |
| celeris | json | 357 | 101388 | 2.15 | 30.0 |
| celeris | params | 771 | 3010 | 2353.85 | 22.0 |
