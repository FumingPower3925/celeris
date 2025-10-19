# Celeris Benchmark Suite

This directory contains comparative benchmarks for Celeris against other popular Go web frameworks.

## Running Benchmarks

To run all benchmarks:

```bash
cd test/benchmark
go test -bench=. -benchmem -benchtime=5s
```

To run specific benchmarks:

```bash
go test -bench=BenchmarkFrameworks/Celeris -benchmem
```

To generate a comparison table:

```bash
go test -bench=. -benchmem -benchtime=5s | tee results.txt
```

## Benchmark Categories

1. **Simple Request** - Basic string response
2. **JSON Response** - JSON encoding and response
3. **Route Parameters** - Parameter extraction from routes

## Frameworks Compared

- **Celeris** - This framework (HTTP/2 native)
- **net/http** - Go standard library with HTTP/2
- **Fiber** - Fast Express-inspired framework (planned)
- **Gin** - High-performance framework (planned)
- **Echo** - Minimalist framework (planned)
- **Chi** - Lightweight router (planned)

## Notes

- Tests are randomized to prevent order effects
- Each benchmark uses unique ports to avoid conflicts
- HTTP/2 is used consistently across all frameworks for fair comparison
- Parallel execution is used to simulate realistic load

## Understanding Results

The benchmarks measure:
- **ns/op** - Nanoseconds per operation (lower is better)
- **B/op** - Bytes allocated per operation (lower is better)
- **allocs/op** - Number of allocations per operation (lower is better)

## Adding More Frameworks

To add a new framework to the comparison:

1. Add the framework as a dependency in `go.mod`
2. Create benchmark functions following the naming pattern
3. Add to the randomized test suite in `comparative_test.go`
4. Update this README with the new framework

## Example Output

```
BenchmarkFrameworks/Celeris_SimpleRequest-8         100000      12345 ns/op      1234 B/op      12 allocs/op
BenchmarkFrameworks/NetHTTP_SimpleRequest-8         90000       13456 ns/op      1345 B/op      13 allocs/op
```

Lower numbers indicate better performance.

