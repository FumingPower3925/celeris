.PHONY: all build test test-extended test-unit test-integration test-benchmark test-fuzz test-coverage test-race test-bench lint docs docs-serve sonar snyk h2spec clean help test-rampup test-rampup-h1 test-rampup-h2 test-rampup-h1-celeris test-rampup-h2-celeris

# Default target
all: lint test build

# Build the example server
build:
	@echo "Building example server..."
	@go build -tags "poll_opt gc_opt" -o bin/example ./cmd/example

# Run tests
test: test-race test-unit test-integration test-bench
	@echo "All test targets completed successfully!"

# Run extended tests
test-extended: test test-rampup test-fuzz test-load
	@echo "All extended test targets completed successfully!"

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
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
	@cd test/benchmark && go test -bench=. -benchmem

# Benchmark targets
.PHONY: bench-sync-h1 bench-sync-h2 bench-sync-hybrid bench-async-h1 bench-async-h2 bench-async-hybrid bench-all
bench-sync-h1:
	@echo "Running Sync HTTP/1.1 Benchmarks..."
	@cd test/benchmark/sync/http1 && go run main.go

bench-sync-h2:
	@echo "Running Sync HTTP/2 Benchmarks..."
	@cd test/benchmark/sync/http2 && go run main.go

bench-sync-hybrid:
	@echo "Running Sync Hybrid Benchmarks..."
	@cd test/benchmark/sync/hybrid && go run main.go

bench-async-h1:
	@echo "Running Async HTTP/1.1 Benchmarks..."
	@cd test/benchmark/async/http1 && go run main.go

bench-async-h2:
	@echo "Running Async HTTP/2 Benchmarks..."
	@cd test/benchmark/async/http2 && go run main.go

bench-async-hybrid:
	@echo "Running Async Hybrid Benchmarks..."
	@cd test/benchmark/async/hybrid && go run main.go

bench-all: bench-sync-h1 bench-sync-h2 bench-sync-hybrid bench-async-h1 bench-async-h2 bench-async-hybrid

# Run incremental ramp-up benchmarks (both HTTP/1.1 and HTTP/2)
test-rampup: test-rampup-h1 test-rampup-h2

# Run HTTP/1.1 ramp-up benchmarks (all frameworks)
test-rampup-h1:
	@echo "Running HTTP/1.1 ramp-up benchmarks (all frameworks)..."
	@cd test/benchmark/async/http1 && go build -tags "poll_opt gc_opt" -o bench-rampup-h1 . && ./bench-rampup-h1

# Run HTTP/1.1 ramp-up benchmarks (Celeris only)
test-rampup-h1-celeris:
	@echo "Running HTTP/1.1 ramp-up benchmarks (Celeris only)..."
	@cd test/benchmark/async/http1 && go build -tags "poll_opt gc_opt" -o bench-rampup-h1 . && FRAMEWORK=celeris ./bench-rampup-h1

# Run HTTP/2 ramp-up benchmarks (all frameworks)
test-rampup-h2:
	@echo "Running HTTP/2 ramp-up benchmarks (all frameworks)..."
	@cd test/benchmark/async/http2 && go build -tags "poll_opt gc_opt" -o bench-rampup-h2 . && ./bench-rampup-h2

# Run HTTP/2 ramp-up benchmarks (Celeris only)
test-rampup-h2-celeris:
	@echo "Running HTTP/2 ramp-up benchmarks (Celeris only)..."
	@cd test/benchmark/async/http2 && go build -tags "poll_opt gc_opt" -o bench-rampup-h2 . && FRAMEWORK=celeris ./bench-rampup-h2

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

# Run benchmarks
test-bench:
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
	echo "Running golangci-lint on test/load..."
	@cd test/load && golangci-lint run
	@$(MAKE) lint-examples

# Lint examples specifically
lint-examples:
	@echo "Running golangci-lint on examples..."
	@echo "Linting examples/health-check/..." && (cd examples/health-check && golangci-lint run) && \
		echo "Linting examples/logger/..." && (cd examples/logger && golangci-lint run) && \
		echo "Linting examples/rate-limiting/..." && (cd examples/rate-limiting && golangci-lint run) && \
		echo "Linting examples/recovery/..." && (cd examples/recovery && golangci-lint run) && \
		echo "Linting examples/request-id/..." && (cd examples/request-id && golangci-lint run) && \
		echo "Linting examples/server-push/..." && (cd examples/server-push && golangci-lint run) && \
		echo "Linting examples/streaming/..." && (cd examples/streaming && golangci-lint run) && \
		echo "Linting examples/http1-only/..." && (cd examples/http1-only && golangci-lint run) && \
		echo "Linting examples/http2-only/..." && (cd examples/http2-only && golangci-lint run) && \
		echo "Linting examples/basic-routing/..." && (cd examples/basic-routing && golangci-lint run) && \
		echo "Linting examples/compression/..." && (cd examples/compression && golangci-lint run) && \
		echo "All example linting completed successfully!"

# Test examples by building them
test-examples:
	@echo "Verifying examples build..."
	@for dir in examples/*/; do \
		if [ -f "$$dir/main.go" ]; then \
			echo "Building $$dir..."; \
			(cd "$$dir" && go build -o /dev/null main.go) || exit 1; \
		fi; \
	done
	@echo "All examples built successfully!"

# Build documentation
docs:
	@echo "Building documentation..."
	@cd docs && hugo

# Serve documentation locally
docs-serve:
	@echo "Serving documentation at http://localhost:1313"
	@cd docs && hugo server -D

# Run SonarQube analysis
sonar: lint
	@echo "Running SonarQube scanner..."
	@sonar-scanner

# Run Snyk security scan
snyk:
	@echo "Running Snyk security scan..."
	@snyk test --severity-threshold=high

# Run incremental load tests for reliability
test-load:
	@echo "Running incremental load tests (rampup-style)..."
	@cd test/load && go mod tidy > /dev/null 2>&1
	@echo ""
	@echo "=== HTTP/1.1 Incremental Load Test (1 client every 100ms for 25s) ==="
	@cd test/load && go build -tags "poll_opt gc_opt" -o load-test . && ./load-test -protocol http1 -addr localhost:8081 -duration 25s -clients 1 -rampup 100ms -delay 2ms -max-conn 50000 2>/dev/null && rm -f load-test
	@echo ""
	@echo "=== HTTP/2.0 Incremental Load Test (1 client every 100ms for 25s) ==="
	@cd test/load && go build -tags "poll_opt gc_opt" -o load-test . && ./load-test -protocol http2 -addr localhost:8082 -duration 25s -clients 1 -rampup 100ms -delay 2ms -max-conn 50000 2>/dev/null && rm -f load-test
	@echo ""
	@echo "=== Mixed Protocol Incremental Load Test (1 client every 100ms for 25s) ==="
	@cd test/load && go build -tags "poll_opt gc_opt" -o load-test . && ./load-test -protocol mixed -addr localhost:8083 -duration 25s -clients 1 -rampup 100ms -delay 2ms -max-conn 50000 2>/dev/null && rm -f load-test
	@echo ""
	@echo "Load tests completed"

# Run HTTP/2 compliance test
h2spec: clean
	@echo "Starting h2spec compliance test..."
	@go run -tags "poll_opt gc_opt" ./cmd/test-server > .server.log 2>&1 & echo $$! > .server.pid
	@sleep 5
	@echo "Running h2spec..."
	@h2spec --strict -S -h 127.0.0.1 -p 18081 || true
	@-if [ -f .server.pid ]; then kill `cat .server.pid` 2>/dev/null || true; rm -f .server.pid; fi

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@echo "Killing running servers..."
	@-pkill -f "test-server" 2>/dev/null || true
	@-pkill -f "^main$$" 2>/dev/null || true
	@-pkill -f "^example$$" 2>/dev/null || true
	@-pkill -f "bench-rampup-h1" 2>/dev/null || true
	@-pkill -f "bench-rampup-h2" 2>/dev/null || true
	@-pkill -f "load-test" 2>/dev/null || true
	@-pkill -f "celeris-test" 2>/dev/null || true
	@-pkill -f "cmd/test-server" 2>/dev/null || true
	@-pkill -f "cmd/example" 2>/dev/null || true
	@echo "Removing log files..."
	@find . -type f -name "*.log" -not -path "./.git/*" -not -path "./docs/themes/*" -delete 2>/dev/null || true
	@echo "Removing binaries..."
	@rm -rf bin/
	@rm -f main test-server celeris-test
	@rm -f cmd/test-server/test-server
	@rm -f cmd/example/example
	@rm -f test/benchmark/async/http1/bench-rampup-h1
	@rm -f test/benchmark/async/http2/bench-rampup-h2
	@rm -f test/load/load-test
	@find . -type f -executable -not -path "./.git/*" -not -path "./docs/themes/*" -not -path "./node_modules/*" -not -name "*.go" -not -name "*.md" -not -name "*.sh" -not -name "Makefile" -not -name "*.toml" -not -name "*.yaml" -not -name "*.json" -not -name "*.yml" -not -name "*.scss" -not -name "*.css" -not -name "*.js" -not -name "*.html" -not -name "*.ttf" -not -name "*.woff" -not -name "*.woff2" -not -name "*.png" -not -name "*.svg" -not -name "*.cast" -not -name "*.csv" -not -name "*.out" -not -name "*.pid" -not -name "go" -not -name "git" -not -name "hugo" -not -name "golangci-lint" -not -name "h2spec" -not -name "snyk" -not -name "sonar-scanner" -delete 2>/dev/null || true
	@rm -f coverage.out coverage.html
	@rm -rf .sonar/
	@rm -f .server.pid
	@echo "Removing profiling and result files..."
	@rm -f *.pprof
	@rm -f *.csv
	@rm -f rampup_results.json
	@rm -f rampup_results.md
	@find test -type f -name "*_results.json" -delete 2>/dev/null || true
	@rm -rf docs/public/
	@echo "Clean complete"

# Display help
help:
	@echo "Celeris - High-Performance HTTP/1.1 & HTTP/2 Server"
	@echo ""
	@echo "Available targets:"
	@echo "  make build             - Build the example server"
	@echo "  make test              - Run all core tests (race, unit, integration, bench)"
	@echo "  make test-extended     - Run extended tests (test + rampup + fuzz)"
	@echo "  make test-unit         - Run unit tests only"
	@echo "  make test-integration  - Run integration tests"
	@echo "  make test-benchmark    - Run comparative benchmarks"
	@echo "  make test-rampup            - Run ramp-up benchmarks (both H1 and H2, all frameworks)"
	@echo "  make test-rampup-h1         - Run HTTP/1.1 ramp-up benchmarks (all frameworks)"
	@echo "  make test-rampup-h1-celeris - Run HTTP/1.1 ramp-up benchmarks (Celeris only, fast)"
	@echo "  make test-rampup-h2         - Run HTTP/2 ramp-up benchmarks (all frameworks)"
	@echo "  make test-rampup-h2-celeris - Run HTTP/2 ramp-up benchmarks (Celeris only, fast)"
	@echo "  make test-load         - Run comprehensive load tests with increasing load"
	@echo "  make test-fuzz         - Run fuzz tests"
	@echo "  make test-coverage     - Generate test coverage report"
	@echo "  make test-race         - Run tests with race detector"
	@echo "  make test-bench        - Run benchmarks"
	@echo "  make lint              - Run linter"
	@echo "  make docs              - Build documentation"
	@echo "  make docs-serve        - Serve documentation locally"
	@echo "  make sonar             - Run SonarQube analysis"
	@echo "  make snyk              - Run Snyk security scan"
	@echo "  make h2spec            - Run HTTP/2 compliance test"
	@echo "  make clean             - Clean build artifacts"
	@echo "  make help              - Display this help message"

