.PHONY: all build test test-unit test-integration test-benchmark test-fuzz test-coverage test-race bench lint coverage docs docs-serve sonar snyk load-test h2spec clean help

# Default target
all: lint test build

# Build the example server
build:
	@echo "Building example server..."
	@go build -o bin/example ./cmd/example

# Run tests
test:
	@echo "Running tests..."
	@go test -v -race ./...

# Run unit tests only
test-unit:
	@echo "Running unit tests..."
	@cd pkg/celeris && go test -v -timeout 2m

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	@cd test/integration && go test -v -timeout 10m

# Run comparative benchmarks
test-benchmark:
	@echo "Running comparative benchmarks..."
	@cd test/benchmark && go test -bench=. -benchmem -benchtime=3s

# Run incremental ramp-up benchmarks
test-rampup:
	@echo "Running incremental ramp-up benchmarks..."
	@cd test/benchmark/cmd/bench && go build -tags "poll_opt gc_opt" -o bench-rampup . && ./bench-rampup

# Run fuzz tests (30s each)
test-fuzz:
	@echo "Running fuzz tests (30s each)..."
	@cd test/fuzzy && go test -fuzz=FuzzRouterPaths -fuzztime=30s || true
	@cd test/fuzzy && go test -fuzz=FuzzContextJSON -fuzztime=30s || true
	@cd test/fuzzy && go test -fuzz=FuzzHeaders_SetGet -fuzztime=30s || true
	@echo "Fuzz tests completed"

# Generate test coverage (unit tests only)
test-coverage:
	@echo "Generating test coverage..."
	@cd pkg/celeris && go test -coverprofile=coverage.out -covermode=atomic
	@go tool cover -html=pkg/celeris/coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	@go test -v -race ./...

# Run benchmarks
bench:
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem ./...

# Run linter
lint:
	@echo "Running golangci-lint on main codebase..."
	@golangci-lint run
	@echo "Running golangci-lint on test/benchmark..."
	@cd test/benchmark && golangci-lint run
	@echo "Running golangci-lint on test/fuzzy..."
	@cd test/fuzzy && golangci-lint run
	@echo "Running golangci-lint on test/integration..."
	@cd test/integration && golangci-lint run
	@echo "All linting completed successfully!"

# Generate coverage report
coverage:
	@echo "Generating coverage report..."
	@go test -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Build documentation
docs:
	@echo "Building documentation..."
	@cd docs && hugo

# Serve documentation locally
docs-serve:
	@echo "Serving documentation at http://localhost:1313"
	@cd docs && hugo server -D

# Run SonarQube analysis
sonar: lint coverage
	@echo "Running SonarQube scanner..."
	@sonar-scanner

# Run Snyk security scan
snyk:
	@echo "Running Snyk security scan..."
	@snyk test --severity-threshold=high

# Run load test
load-test:
	@echo "Starting load test..."
	@go run ./cmd/example > /dev/null 2>&1 & echo $$! > .server.pid
	@sleep 3
	@echo "Running h2load..."
	@h2load -n 10000 -c 100 -m 10 http://localhost:8080/ || true
	@kill `cat .server.pid` && rm .server.pid

# Run HTTP/2 compliance test
h2spec:
	@echo "Starting h2spec compliance test..."
	@go run -tags "poll_opt gc_opt" ./cmd/test-server > /dev/null 2>&1 & echo $$! > .server.pid
	@sleep 3
	@echo "Running h2spec..."
	@h2spec --strict -S -h 127.0.0.1 -p 8080 || true
	@-if [ -f .server.pid ]; then kill `cat .server.pid` 2>/dev/null || true; rm -f .server.pid; fi

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -f coverage.out coverage.html
	@rm -rf .sonar/
	@rm -f .server.pid
	@rm -rf docs/public/
	@echo "Clean complete"

# Display help
help:
	@echo "Celeris - High-Performance HTTP/2 Server"
	@echo ""
	@echo "Available targets:"
	@echo "  make build             - Build the example server"
	@echo "  make test              - Run all tests (unit + integration)"
	@echo "  make test-unit         - Run unit tests only"
	@echo "  make test-integration  - Run integration tests"
	@echo "  make test-benchmark    - Run comparative benchmarks"
	@echo "  make test-rampup       - Run incremental ramp-up benchmarks"
	@echo "  make test-fuzz         - Run fuzz tests"
	@echo "  make test-coverage     - Generate test coverage report"
	@echo "  make test-race         - Run tests with race detector"
	@echo "  make bench             - Run benchmarks"
	@echo "  make lint              - Run linter"
	@echo "  make coverage          - Generate coverage report (all)"
	@echo "  make docs              - Build documentation"
	@echo "  make docs-serve        - Serve documentation locally"
	@echo "  make sonar             - Run SonarQube analysis"
	@echo "  make snyk              - Run Snyk security scan"
	@echo "  make load-test         - Run load test with h2load"
	@echo "  make h2spec            - Run HTTP/2 compliance test"
	@echo "  make clean             - Clean build artifacts"
	@echo "  make help              - Display this help message"

