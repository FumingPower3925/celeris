# Celeris Integration Tests

This directory contains comprehensive integration tests for the Celeris framework that test real-world scenarios and end-to-end functionality.

## Purpose

Integration tests verify that different components of Celeris work together correctly:
- HTTP/2 protocol handling
- Request routing and parameter extraction
- Middleware execution
- Concurrent request handling
- Response generation
- Error handling

## Running Tests

To run all integration tests:

```bash
cd test/integration
go test -v
```

To run specific test files:

```bash
go test -v -run TestBasicRequest
go test -v -run TestConcurrent
```

To run with race detection:

```bash
go test -v -race
```

To run with coverage:

```bash
go test -v -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Test Categories

### Basic Tests (`basic_test.go`)

Tests fundamental HTTP/2 server functionality

### Middleware Tests (`middleware_test.go`)

Tests middleware functionality

### Concurrent Tests (`concurrent_test.go`)

Tests concurrent request handling

### Advanced Tests (`advanced_test.go`)

Tests advanced features

## Best Practices

1. **Isolate** - Each test should be independent
2. **Clean up** - Always defer server.Stop()
3. **Wait** - Allow time for server startup
4. **Verify** - Check status codes, headers, and bodies
5. **Handle errors** - Use proper error checking

## Resources

- [Go Testing Documentation](https://golang.org/pkg/testing/)
- [HTTP/2 Specification](https://http2.github.io/)
