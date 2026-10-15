#!/usr/bin/env bash
#
# Quality gate for terraform_ast.
#
# Runs the unit suite with a coverage profile, fails unless every package and the
# module as a whole reach 100% statement coverage, then smoke-runs every fuzz
# target so new inputs keep exercising the parsers.
#
# Usage: scripts/verify.sh [fuzz-time-seconds]

set -euo pipefail

cd "$(dirname "$0")/.."

FUZZ_SECONDS="${1:-10}"
PROFILE="$(mktemp -t coverage.XXXXXX)"
trap 'rm -f "$PROFILE"' EXIT

echo "==> gofmt"
unformatted="$(gofmt -l . | grep -v '^$' || true)"
if [ -n "$unformatted" ]; then
  echo "unformatted files:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> go vet"
go vet ./...

if command -v golangci-lint >/dev/null 2>&1; then
  echo "==> golangci-lint (govet, errcheck, staticcheck, unused, gosec)"
  golangci-lint run --timeout 10m ./...
else
  echo "==> golangci-lint not installed, skipping"
fi

echo "==> unit tests with coverage"
go test -covermode=atomic -coverprofile="$PROFILE" ./...

echo "==> coverage gate (100% required)"
total="$(go tool cover -func="$PROFILE" | tail -1 | awk '{print $NF}')"
if [ "$total" != "100.0%" ]; then
  echo "total statement coverage is $total, expected 100.0%" >&2
  go tool cover -func="$PROFILE" | grep -v '100.0%' >&2
  exit 1
fi
echo "total statement coverage: $total"

if [ "${RACE:-0}" = "1" ]; then
  echo "==> race detector"
  go test -race ./...
fi

echo "==> fuzz smoke run (${FUZZ_SECONDS}s per target)"
targets="$(grep -rhoE '^func Fuzz[A-Za-z0-9_]+\(' --include='*_test.go' . | sed "s/^func //; s/($//" | sort -u)"
for target in $targets; do
  package="$(grep -rlE "^func ${target}\(" --include='*_test.go' . | head -1 | xargs dirname)"
  printf '  %-28s %s\n' "$target" "$package"
  go test "./${package}" -run "^${target}$" -fuzz "^${target}$" -fuzztime "${FUZZ_SECONDS}s" >/dev/null
done

echo "==> all checks passed"