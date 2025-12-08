#!/bin/bash
# set -e removed to allow continuation on failure

FRAMEWORKS=("celeris" "nethttp" "gin" "echo" "chi" "fiber" "iris")
BENCHMARKS=("bench-sync-h1" "bench-sync-h2" "bench-sync-hybrid" "bench-async-h1" "bench-async-h2" "bench-async-hybrid")

mkdir -p test/benchmark/results

echo "Starting full benchmark suite at $(date)"
echo "Current directory: $(pwd)"

for fw in "${FRAMEWORKS[@]}"; do
    echo "========================================================"
    echo "Testing Framework: $fw"
    echo "========================================================"
    
    for bench in "${BENCHMARKS[@]}"; do
        echo "Running $bench for $fw..."
        
        # Run benchmark
        # We capture stdout/stderr to a file
        export FRAMEWORK=$fw
        if make $bench > "test/benchmark/results/${fw}_${bench}.log" 2>&1; then
            echo "  ✓ $bench completed"
        else
            echo "  ✗ $bench failed"
        fi
        
        # Move generated result files to results dir with unique names
        # The benchmarks generate files like *_results.json in the subdirectories
        # We need to find them and move them
        
        # Determine source directory based on benchmark name
        case $bench in
            "bench-sync-h1") src_dir="test/benchmark/sync/http1" ;;
            "bench-sync-h2") src_dir="test/benchmark/sync/http2" ;;
            "bench-sync-hybrid") src_dir="test/benchmark/sync/hybrid" ;;
            "bench-async-h1") src_dir="test/benchmark/async/http1" ;;
            "bench-async-h2") src_dir="test/benchmark/async/http2" ;;
            "bench-async-hybrid") src_dir="test/benchmark/async/hybrid" ;;
        esac
        
        # Move JSON/CSV files if they exist
        if ls $src_dir/*_results.json 1> /dev/null 2>&1; then
            mv $src_dir/*_results.json "test/benchmark/results/${fw}_${bench}.json"
        fi
        if ls $src_dir/*_results.csv 1> /dev/null 2>&1; then
            mv $src_dir/*_results.csv "test/benchmark/results/${fw}_${bench}.csv"
        fi
        
        # Cool down
        sleep 2
    done
done

echo "Benchmark suite completed. Results in results/"
