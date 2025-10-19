# Celeris Fuzz Testing Suite

This directory contains comprehensive fuzz tests for the Celeris framework to discover edge cases, security vulnerabilities, and unexpected behavior.

## What is Fuzz Testing?

Fuzz testing is an automated testing technique that provides invalid, unexpected, or random data as inputs to discover bugs, security vulnerabilities, and crashes.

## Running Fuzz Tests

To run all fuzz tests:

```bash
cd test/fuzzy
go test -fuzz=. -fuzztime=30s
```

To run a specific fuzz test:

```bash
go test -fuzz=FuzzRouterPaths -fuzztime=1m
```

To run with longer duration:

```bash
go test -fuzz=FuzzContextJSON -fuzztime=10m
```

## Fuzz Test Categories

### Router Fuzzing (`router_fuzz_test.go`)

- **FuzzRouterPaths** - Tests router with random URL paths
  - Edge cases: empty paths, very long paths, special characters
  - Security: path traversal attempts, null bytes
  
- **FuzzRouterMethods** - Tests with various HTTP methods
  - Invalid methods, case variations, extremely long methods
  
- **FuzzRouteParameters** - Tests parameter extraction
  - Special characters, path separators, encoding issues
  
- **FuzzWildcardPaths** - Tests wildcard route matching
  - Nested paths, traversal attempts, edge cases
  
- **FuzzRouteDefinition** - Tests route registration
  - Invalid patterns, edge cases in route definitions

### Context Fuzzing (`context_fuzz_test.go`)

- **FuzzContextJSON** - Tests JSON encoding with random data
  - Various JSON structures, edge cases, invalid data
  
- **FuzzContextString** - Tests string responses
  - Special characters, format strings, encoding issues
  
- **FuzzContextHTML** - Tests HTML responses
  - XSS attempts, malformed HTML, edge cases
  
- **FuzzHeaderOperations** - Tests header manipulation
  - Special characters, very long headers, edge cases
  
- **FuzzContextSetGet** - Tests context value storage
  - Key collisions, special characters, large values
  
- **FuzzContextBindJSON** - Tests JSON binding
  - Invalid JSON, edge cases, type mismatches
  
- **FuzzStatusCodes** - Tests various HTTP status codes
  - Invalid codes, edge cases, boundary conditions

### Headers Fuzzing (`headers_fuzz_test.go`)

- **FuzzHeaders_SetGet** - Tests basic header operations
- **FuzzHeaders_Del** - Tests header deletion
- **FuzzHeaders_MultipleOperations** - Tests complex scenarios
- **FuzzHeaders_SpecialCharacters** - Tests special characters
- **FuzzHeaders_CaseSensitivity** - Tests case handling
- **FuzzHeaders_UpdateExisting** - Tests header updates

## Understanding Results

### Finding Crashes

When a fuzz test finds a crash, it saves the failing input in `testdata/fuzz/`:

```
testdata/fuzz/FuzzRouterPaths/1234567890abcdef
```

To reproduce:

```bash
go test -run=FuzzRouterPaths
```

### Coverage

To see code coverage:

```bash
go test -fuzz=FuzzRouterPaths -fuzztime=30s -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Best Practices

1. **Run regularly** - Include fuzz tests in CI/CD pipeline
2. **Run long** - Longer fuzz times find more bugs
3. **Fix all crashes** - Every crash is a potential security issue
4. **Add seeds** - Add interesting test cases to seed corpus
5. **Monitor memory** - Watch for memory leaks

## Corpus Management

The fuzz test corpus grows over time. To reset:

```bash
rm -rf testdata/fuzz/
```

To share interesting findings, commit corpus files to git.

## Integration with CI/CD

For CI/CD pipelines, run with a time limit:

```bash
go test -fuzz=. -fuzztime=30s -parallel=4
```

## Security Notes

Fuzz tests are particularly important for:
- Input validation
- Security boundaries
- Edge cases in parsing
- Memory safety
- Panic recovery

Any panic or crash found by fuzz testing should be investigated as a potential security issue.

## Adding New Fuzz Tests

To add a new fuzz test:

1. Create a function starting with `Fuzz`
2. Add seed corpus with `f.Add()`
3. Implement the test logic in `f.Fuzz()`
4. Handle expected panics gracefully
5. Document what you're testing

Example:

```go
func FuzzMyFeature(f *testing.F) {
    // Seed corpus
    f.Add("test input")
    
    f.Fuzz(func(t *testing.T, input string) {
        // Skip invalid inputs
        if !utf8.ValidString(input) {
            t.Skip("invalid UTF-8")
        }
        
        // Test your feature
        result := MyFeature(input)
        
        // Verify invariants
        if result == nil {
            t.Error("unexpected nil result")
        }
    })
}
```

## Resources

- [Go Fuzzing Documentation](https://go.dev/security/fuzz/)
- [Fuzzing Best Practices](https://go.dev/doc/fuzz/)
- [OSS-Fuzz](https://github.com/google/oss-fuzz)

