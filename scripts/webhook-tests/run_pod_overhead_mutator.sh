#!/bin/bash

# Test runner script for pod overhead mutator feature
# This script runs all the unit tests for the memory overhead annotation implementation

# Don't exit on first error - we want to run all tests and report overall status
set +e

# Track overall test status
OVERALL_SUCCESS=true
POD_MUTATOR_SUCCESS=false
KATACONFIG_SUCCESS=false
INTEGRATION_SUCCESS=false
BENCHMARK_SUCCESS=false
COVERAGE_SUCCESS=false
LINTING_SUCCESS=false
RACE_SUCCESS=false

echo "Running Pod Overhead Mutator Tests"
echo "=================================="

# Check if we're in the right directory
if [ ! -f "go.mod" ]; then
    echo "ERROR: Please run this script from the operator root directory"
    exit 1
fi

# Check if Go is available
if ! command -v go &> /dev/null; then
    echo "ERROR: Go is not installed or not in PATH"
    exit 1
fi

# Set test environment variables
export GO111MODULE=on
export CGO_ENABLED=0

echo "1. Running pod mutator tests..."
echo "-------------------------------"
if go test -v ./controllers -run TestPodMutator -timeout 30s; then
    echo "✓ Pod mutator tests passed"
    POD_MUTATOR_SUCCESS=true
else
    echo "❌ Pod mutator tests failed"
    POD_MUTATOR_SUCCESS=false
    OVERALL_SUCCESS=false
fi

echo ""
echo "2. Running KataConfig webhook tests..."
echo "--------------------------------------"
if go test -v ./api/v1 -run TestKataConfig -timeout 30s; then
    echo "✓ KataConfig webhook tests passed"
    KATACONFIG_SUCCESS=true
else
    echo "❌ KataConfig webhook tests failed"
    KATACONFIG_SUCCESS=false
    OVERALL_SUCCESS=false
fi

echo ""
echo "3. Running all memory overhead related tests..."
echo "----------------------------------------------"
if go test -v ./... -run "MemoryOverhead|PodMutator|KataConfig" -timeout 60s; then
    echo "✓ All memory overhead tests passed"
    INTEGRATION_SUCCESS=true
else
    echo "❌ Some memory overhead tests failed"
    INTEGRATION_SUCCESS=false
    OVERALL_SUCCESS=false
fi

echo ""
echo "4. Running benchmark tests..."
echo "----------------------------"
if go test -v ./controllers -run Benchmark -bench=. -timeout 60s; then
    echo "✓ Benchmark tests completed"
    BENCHMARK_SUCCESS=true
else
    echo "❌ Benchmark tests failed"
    BENCHMARK_SUCCESS=false
    OVERALL_SUCCESS=false
fi

echo ""
echo "5. Running coverage analysis..."
echo "------------------------------"
COVERAGE_SUCCESS=true

if go test -v ./controllers -run TestPodMutator -coverprofile=pod_mutator_coverage.out; then
    echo "✓ Pod mutator coverage generated"
    go tool cover -func=pod_mutator_coverage.out | tail -1
else
    echo "❌ Pod mutator coverage failed"
    COVERAGE_SUCCESS=false
    OVERALL_SUCCESS=false
fi

if go test -v ./api/v1 -run TestKataConfig -coverprofile=kataconfig_coverage.out; then
    echo "✓ KataConfig coverage generated"
    go tool cover -func=kataconfig_coverage.out | tail -1
else
    echo "❌ KataConfig coverage failed"
    COVERAGE_SUCCESS=false
    OVERALL_SUCCESS=false
fi

echo ""
echo "6. Running linting checks..."
echo "---------------------------"
if command -v golangci-lint &> /dev/null; then
    LINTING_SUCCESS=true

    # Lint controllers directory
    echo "  Linting controllers directory..."
    if golangci-lint run ./controllers/ --disable=typecheck --enable=gofmt,goimports,govet,ineffassign,staticcheck,unused,errcheck,gosimple,goconst,misspell; then
        echo "  ✓ Controllers linting passed"
    else
        echo "  ❌ Controllers linting failed"
        LINTING_SUCCESS=false
        OVERALL_SUCCESS=false
    fi

    # Lint api/v1 directory
    echo "  Linting api/v1 directory..."
    if golangci-lint run ./api/v1/ --disable=typecheck --enable=gofmt,goimports,govet,ineffassign,staticcheck,unused,errcheck,gosimple,goconst,misspell; then
        echo "  ✓ API/v1 linting passed"
    else
        echo "  ❌ API/v1 linting failed"
        LINTING_SUCCESS=false
        OVERALL_SUCCESS=false
    fi

    if $LINTING_SUCCESS; then
        echo "✓ All linting checks passed"
    else
        echo "❌ Some linting checks failed"
    fi
else
    echo "⚠️  golangci-lint not found, skipping linting checks"
    LINTING_SUCCESS=true  # Skip is considered success
fi

echo ""
echo "7. Running race detection tests..."
echo "---------------------------------"
# Enable CGO for race detection
export CGO_ENABLED=1
if go test -v ./controllers -run TestPodMutator -race -timeout 60s; then
    echo "✓ Race detection tests passed"
    RACE_SUCCESS=true
else
    echo "❌ Race detection tests failed"
    RACE_SUCCESS=false
    OVERALL_SUCCESS=false
fi
# Reset CGO_ENABLED for consistency
export CGO_ENABLED=0

# Function to display test summary with status
test_summary() {
    local test_name="$1"
    local success_var="$2"
    
    if $success_var; then
        echo "- $test_name: ✓"
    else
        echo "- $test_name: ❌"
    fi
}

echo ""
# Report overall status
if $OVERALL_SUCCESS; then
    echo "🎉 All tests completed successfully!"
else
    echo "❌ Some tests failed!"
fi
echo ""
echo "Test Summary:"
test_summary "Pod mutator unit tests" $POD_MUTATOR_SUCCESS
test_summary "KataConfig webhook tests" $KATACONFIG_SUCCESS
test_summary "Memory overhead integration tests" $INTEGRATION_SUCCESS
test_summary "Benchmark tests" $BENCHMARK_SUCCESS
test_summary "Coverage analysis" $COVERAGE_SUCCESS
test_summary "Linting checks" $LINTING_SUCCESS
test_summary "Race detection" $RACE_SUCCESS
echo ""
if $OVERALL_SUCCESS; then
    echo "The feature appears to work as expected."
else
    echo "Please fix the failing tests before proceeding."
    exit 1
fi
