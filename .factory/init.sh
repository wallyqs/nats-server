#!/bin/bash
set -e

# Ensure Go toolchain is available
go version > /dev/null 2>&1 || { echo "Go is not installed"; exit 1; }

# Create conf/v2 directory if it doesn't exist
mkdir -p conf/v2

# Verify v1 tests still pass (baseline)
echo "Verifying v1 parser baseline..."
go test ./conf/... -count=1 -short > /dev/null 2>&1 && echo "v1 baseline: OK" || echo "v1 baseline: WARN - some tests may fail"
