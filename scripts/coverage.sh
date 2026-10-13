#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
coverage_root="$PWD"
coverage_module="${1:-.}"
case "$coverage_module" in
    .) coverage_profile="$coverage_root/.tmp/coverage.out" ;;
    sdk/go) coverage_profile="$coverage_root/.tmp/sdk-go-coverage.out" ;;
    *) printf 'Unsupported coverage module: %s\n' "$coverage_module" >&2; exit 1 ;;
esac
mkdir -p .tmp/go-tmp .tmp/tmp
export GOCACHE="$PWD/.tmp/go-build" GOMODCACHE="$PWD/.tmp/go-mod" GOPATH="$PWD/.tmp/go"
export GOTMPDIR="$PWD/.tmp/go-tmp" TMPDIR="$PWD/.tmp/tmp"
export GOWORK=off

# Repository policy owns the gate; inherited overrides cannot remove its checks.
unset COVERPROFILE MODULE_PREFIX PROJECT_NAME HTML_OUT \
    THRESHOLD_FUNC THRESHOLD_PACKAGE THRESHOLD_PRINT THRESHOLD_TOTAL UNCOVERED_LIMIT \
    EXCLUDE_PACKAGES SKIP_RESULT_PACKAGES EXCLUDE_FILES EXCLUDE_FUNCS \
    TEST_TIMEOUT BUILD_TAGS GENERATE_HTML RACE_DETECTION FAIL_ON_CRITICAL_BLOCKS

cd "$coverage_module"
exec go run github.com/codetreker/go-cov/cmd/go-cov@v0.1.0 \
    --config "$coverage_root/.go-cov.toml" --ci --coverprofile "$coverage_profile"
