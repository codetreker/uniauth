#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p .tmp/go-tmp .tmp/tmp
export GOCACHE="$PWD/.tmp/go-build" GOMODCACHE="$PWD/.tmp/go-mod" GOPATH="$PWD/.tmp/go"
export GOTMPDIR="$PWD/.tmp/go-tmp" TMPDIR="$PWD/.tmp/tmp"

# Repository policy owns the gate; inherited overrides cannot remove its checks.
unset COVERPROFILE MODULE_PREFIX PROJECT_NAME HTML_OUT \
    THRESHOLD_FUNC THRESHOLD_PACKAGE THRESHOLD_PRINT THRESHOLD_TOTAL UNCOVERED_LIMIT \
    EXCLUDE_PACKAGES SKIP_RESULT_PACKAGES EXCLUDE_FILES EXCLUDE_FUNCS \
    TEST_TIMEOUT BUILD_TAGS GENERATE_HTML RACE_DETECTION FAIL_ON_CRITICAL_BLOCKS

exec go run github.com/codetreker/go-cov/cmd/go-cov@v0.1.0 \
    --config .go-cov.toml --ci --coverprofile .tmp/coverage.out
