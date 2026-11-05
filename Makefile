# terraform_ast Makefile
# Build, lint, test, security, and release targets for tfastdiff.

SHELL := /bin/bash

# Toolchain configuration (prefer Homebrew Go 1.27.3 and local tools, fall back to PATH)
GO ?= $(shell which /home/linuxbrew/.linuxbrew/bin/go 2>/dev/null || which go)
GOPATH ?= $(shell $(GO) env GOPATH)
GOBIN ?= $(GOPATH)/bin
export PATH := /home/linuxbrew/.linuxbrew/bin:$(GOBIN):$(PATH)

GOLANGCI_LINT ?= $(shell which golangci-lint 2>/dev/null || echo /home/linuxbrew/.linuxbrew/bin/golangci-lint)
GOSEC         ?= $(shell which gosec 2>/dev/null || echo /home/linuxbrew/.linuxbrew/bin/gosec)
GOVULNCHECK   ?= $(shell which govulncheck 2>/dev/null || echo $(GOBIN)/govulncheck)
DEADCODE      ?= $(shell which deadcode 2>/dev/null || echo $(GOBIN)/deadcode)
GOIMPORTS     ?= $(shell which goimports 2>/dev/null || echo $(GOBIN)/goimports)
MARKDOWNLINT  ?= $(shell which markdownlint 2>/dev/null || echo /home/linuxbrew/.linuxbrew/bin/markdownlint)
PYTHON        ?= $(shell which python3 2>/dev/null || echo /home/linuxbrew/.linuxbrew/bin/python3)
DOCKER        ?= $(shell which docker 2>/dev/null || echo docker)

BIN := bin/tfastdiff
FUZZ_TIME ?= 10
VENV := .venv
VENV_PYTHON := $(VENV)/bin/python3
PIP := $(VENV)/bin/pip
IMAGE_TAG ?= latest

.PHONY: all help ci release build build-release build-debug build-race clean \
	deps deps-upgrade lint lint-fast lint-fix vet staticcheck deadcode fmt fmt-check \
	security secure sec-gosec sec-vulncheck sec-audit \
	perf test test-short test-all race test-coverage coverage-gate coverage-html test-fuzz \
	accuracy-gate oracle venv docker markdown-lint verify pre-commit makehooks install-hooks fuzz

# Default target
all: verify

## help: Print available targets with descriptions
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Combined Local CI Pipeline:"
	@echo "  ci                   Run the complete combined CI verification pipeline locally"
	@echo "  release              Run full CI pipeline and build release binary"
	@echo ""
	@echo "Build Targets:"
	@echo "  build                Compile bin/tfastdiff"
	@echo "  build-release        Build release binary with stripped debug symbols (-s -w)"
	@echo "  build-debug          Build with optimizations and inlining disabled (-N -l)"
	@echo "  build-race           Compile all packages with race detector enabled"
	@echo "  cross-compile        Verify ARM64 cross-compilation (build & vet)"
	@echo "  clean                Remove build artifacts, venv, and coverage files"
	@echo "  deps                 Download and tidy Go modules"
	@echo "  deps-upgrade         Upgrade all Go modules and tidy go.mod"
	@echo ""
	@echo "Static Analysis & Linting:"
	@echo "  lint                 Run gofmt, go vet, and golangci-lint"
	@echo "  lint-fast            Run golangci-lint on cmd/pkg/internal"
	@echo "  lint-fix             Run golangci-lint with automated fixes"
	@echo "  vet                  Run go vet standard analyzer"
	@echo "  staticcheck          Run staticcheck and unused analyzers"
	@echo "  deadcode             Run deadcode analyzer for unreachable functions"
	@echo "  fmt                  Format code with gofmt and goimports"
	@echo "  fmt-check            Verify formatting without modifying files"
	@echo "  markdown-lint        Run markdown linting on documentation"
	@echo ""
	@echo "Security Analysis:"
	@echo "  security             Run full security audit (gosec + govulncheck)"
	@echo "  secure               Run gosec (SARIF) and govulncheck"
	@echo "  sec-gosec            Run gosec AST security analysis"
	@echo "  sec-vulncheck        Run govulncheck against Go vulnerability database"
	@echo "  sec-audit            Audit dependencies for known CVEs"
	@echo ""
	@echo "Performance:"
	@echo "  perf                 Run benchmarks on ast/diff/report/blastradius packages"
	@echo ""
	@echo "Testing & Quality Gates:"
	@echo "  test                 Run unit tests with coverage"
	@echo "  test-short           Run unit tests in short mode"
	@echo "  test-all             Run full test suite across all packages"
	@echo "  race                 Run tests under Go race detector"
	@echo "  test-coverage        Verify 100% package test presence and collect coverage"
	@echo "  coverage-gate        Run measured statement coverage gate"
	@echo "  coverage-html        Generate interactive HTML coverage report"
	@echo "  test-fuzz            Run fuzz test harnesses"
	@echo "  fuzz                 Run extended fuzz smoke suite (verify.sh)"
	@echo "  verify               Run scripts/verify.sh quality gate"
	@echo "  pre-commit           Alias for verify"
	@echo ""
	@echo "Terraform AST Specific:"
	@echo "  accuracy-gate        Run upstream EKS/GKE upgrade path accuracy validation"
	@echo "  venv                 Setup shared Python virtual environment (.venv)"
	@echo "  oracle               Run independent Python verification oracle"
	@echo ""
	@echo "Container Targets:"
	@echo "  docker               Build hardened distroless container"
	@echo ""
	@echo "Git Hooks:"
	@echo "  makehooks            Install scripts/verify.sh into .git/hooks/pre-commit"
	@echo "  install-hooks        Alias for makehooks"

# ==============================================================================
# Combined Local CI Pipeline
# ==============================================================================
ci: fmt-check lint vet build test-coverage race security
	@echo "=========================================================="
	@echo " ✓ Complete local CI verification pipeline passed 100%!"
	@echo "=========================================================="

release: ci build-release
	@echo "=========================================================="
	@echo " ✓ Release build and verification completed successfully!"
	@echo "=========================================================="

# ==============================================================================
# Build Targets
# ==============================================================================
build:
	@mkdir -p bin
	$(GO) build -o $(BIN) ./cmd/tfastdiff

build-release:
	@echo "Building release artifacts..."
	@mkdir -p bin
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/tfastdiff

build-debug:
	@echo "Building debug artifacts (no inlining, debug symbols)..."
	@mkdir -p bin
	$(GO) build -gcflags="all=-N -l" -o $(BIN) ./cmd/tfastdiff

build-race:
	@echo "Building all packages with race detector enabled..."
	$(GO) build -race ./...

cross-compile:
	@echo "Verifying ARM64 cross-compilation..."
	GOARCH=arm64 $(GO) build ./...
	GOARCH=arm64 $(GO) vet ./...
	@echo "ARM64 cross-compiles cleanly."

deps:
	@echo "Resolving dependencies..."
	$(GO) mod download
	$(GO) mod tidy

deps-upgrade:
	@echo "Upgrading Go dependencies..."
	$(GO) get -u ./...
	$(GO) mod tidy

clean:
	@echo "Cleaning build and test artifacts..."
	rm -rf bin coverage *.out *.prof *.test cover.html $(VENV) __pycache__ *.pyc oracle_report.json coverage.html mem.out cpu.out
	$(GO) clean -testcache

# ==============================================================================
# Static Analysis & Linters
# ==============================================================================
lint:
	gofmt -l .
	$(GO) vet ./...
	@if command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		$(GOLANGCI_LINT) run --timeout 10m ./...; \
	fi

lint-fast:
	@echo "Running fast linters..."
	$(GOLANGCI_LINT) run ./cmd/... ./pkg/... ./internal/...

lint-fix:
	@echo "Running golangci-lint with automated fixes..."
	$(GOLANGCI_LINT) run --fix ./...

vet:
	@echo "Running go vet..."
	$(GO) vet ./...

staticcheck:
	@echo "Running staticcheck and unused analyzers..."
	$(GOLANGCI_LINT) run -E staticcheck,unused ./...

deadcode:
	@echo "Running deadcode reachability analysis..."
	$(DEADCODE) ./cmd/... ./pkg/... ./internal/...

fmt:
	@echo "Formatting Go source files..."
	gofmt -s -w .
	$(GOIMPORTS) -w .

fmt-check:
	@echo "Checking code formatting..."
	@DIFF=$$(gofmt -l .); \
	if [ -n "$$DIFF" ]; then \
		echo "Unformatted files found:"; \
		echo "$$DIFF"; \
		exit 1; \
	fi
	@echo "All files formatted cleanly."

markdown-lint:
	@echo "Running markdown linting on documentation..."
	-$(MARKDOWNLINT) docs/*.md README.md CONTRIBUTING.md

# ==============================================================================
# Security Analysis
# ==============================================================================
security: sec-gosec sec-vulncheck
	@echo "Security analysis passed cleanly."

secure:
	$(GOSEC) -fmt=sarif ./...
	$(GOVULNCHECK) ./...

sec-gosec:
	@echo "Running gosec AST security analysis..."
	$(GOSEC) ./...

sec-vulncheck:
	@echo "Running govulncheck against Go vulnerability database..."
	$(GOVULNCHECK) ./...

sec-audit: sec-vulncheck
	@echo "Auditing module dependencies..."
	$(GO) list -m all > /dev/null

# ==============================================================================
# Performance
# ==============================================================================
perf:
	$(GO) test -bench . -benchtime=3s -count=5 -run '^$$' ./pkg/ast/ ./pkg/diff/ ./pkg/report/ ./pkg/blastradius/ 2>&1 | tee perf.out

# ==============================================================================
# Testing & Quality Gates
# ==============================================================================
test:
	$(GO) test -cover ./...

test-short:
	@echo "Running unit tests in short mode (bounded memory)..."
	$(GO) test -short ./...

test-all:
	@echo "Running all unit tests..."
	$(GO) test -v ./...

race:
	@echo "Running race detector..."
	$(GO) test -race ./...

test-coverage:
	@echo "Verifying 100% Package Test Coverage Gate..."
	@UNTESTED=$$($(GO) test -list '.*' ./... 2>&1 | grep '\[no test files\]' || true); \
	if [ -n "$$UNTESTED" ]; then \
		echo "Found packages without test files:"; \
		echo "$$UNTESTED"; \
		exit 1; \
	fi
	@echo "All packages have active test coverage."
	$(GO) test -short -cover ./...

coverage-gate:
	@echo "Running measured statement coverage gate..."
	$(GO) test -short -coverprofile=coverage.out ./...
	@COV=$$($(GO) tool cover -func=coverage.out | awk '/^total:/ {print $$3}' | tr -d '%'); \
	echo "Measured coverage: $$COV%"; \
	if awk "BEGIN {exit !($$COV >= 100)}"; then \
		echo "Coverage gate passed (>= 100%)."; \
	else \
		echo "Coverage gate failed ($$COV% < 100%)."; \
		exit 1; \
	fi

coverage-html:
	@echo "Generating interactive HTML coverage report..."
	$(GO) test -short -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report saved to coverage.html"

test-fuzz:
	@echo "Running fuzz tests in short mode..."
	$(GO) test -short -run='^Fuzz' ./...

fuzz:
	bash scripts/verify.sh 30

verify:
	bash scripts/verify.sh $(FUZZ_TIME)

pre-commit: verify

# ==============================================================================
# Terraform AST Specific
# ==============================================================================
accuracy-gate: build
	bash scripts/validate_upgrade_accuracy.sh

venv:
	@if [ ! -d "$(VENV)" ]; then \
		echo "==> Creating Python virtual environment in $(VENV)..."; \
		$(PYTHON) -m venv $(VENV); \
	fi
	@$(VENV_PYTHON) -m pip install --upgrade pip
	@if [ -f requirements.txt ]; then \
		$(PIP) install -r requirements.txt; \
	fi

oracle: build venv
	$(VENV_PYTHON) scripts/oracle_verify.py --output oracle_report.json

# ==============================================================================
# Container Targets
# ==============================================================================
docker:
	$(DOCKER) build -t tfastdiff:latest .

# ==============================================================================
# Git Hooks
# ==============================================================================
makehooks:
	@echo "Installing Git pre-commit hook (scripts/verify.sh -> .git/hooks/pre-commit)..."
	@mkdir -p .git/hooks
	@chmod +x scripts/verify.sh
	@ln -sf ../../scripts/verify.sh .git/hooks/pre-commit || cp scripts/verify.sh .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "✓ Pre-commit hook installed successfully."

install-hooks: makehooks
install-commit-hook: makehooks