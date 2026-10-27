.PHONY: all build test verify fuzz lint clean docker secure accuracy-gate venv oracle help

BIN := bin/tfastdiff
FUZZ_TIME ?= 10
VENV := .venv
PYTHON := $(VENV)/bin/python3
PIP := $(VENV)/bin/pip

all: verify

build:
	@mkdir -p bin
	go build -o $(BIN) cmd/tfastdiff/main.go

test:
	go test -cover ./...

verify:
	bash scripts/verify.sh $(FUZZ_TIME)

fuzz:
	bash scripts/verify.sh 30

perf:
	go test -bench . -benchtime=3s -count=5 -run '^$' ./pkg/ast/ ./pkg/diff/ ./pkg/report/ ./pkg/blastradius/ 2>&1 | tee perf.out

lint:
	gofmt -l .
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout 10m ./...; \
	fi

clean:
	rm -rf bin coverage *.out *.prof *.test cover.html $(VENV) __pycache__ *.pyc oracle_report.json

docker:
	docker build -t tfastdiff:latest .

secure:
	gosec -fmt=sarif ./...
	govulncheck ./...

accuracy-gate: build
	bash scripts/validate_upgrade_accuracy.sh

venv:
	@if [ ! -d "$(VENV)" ]; then \
		echo "==> Creating Python virtual environment in $(VENV)..."; \
		python3 -m venv $(VENV); \
	fi
	@$(PYTHON) -m pip install --upgrade pip
	@if [ -f requirements.txt ]; then \
		$(PIP) install -r requirements.txt; \
	fi

oracle: build venv
	$(PYTHON) scripts/oracle_verify.py --output oracle_report.json

help:
	@echo "terraform_ast build targets:"
	@echo "  build         - Compile bin/tfastdiff"
	@echo "  test          - Run unit tests with coverage"
	@echo "  verify        - Run full verification suite (fmt, vet, lint, 100% coverage, fuzz)"
	@echo "  accuracy-gate - Run upstream EKS/GKE upgrade path accuracy validation & confidence scoring"
	@echo "  venv          - Setup shared Python virtual environment (.venv)"
	@echo "  oracle        - Run independent Python verification oracle for accuracy, confidence, and safety"
	@echo "  fuzz          - Run extended fuzz smoke suite"
	@echo "  lint          - Run gofmt, go vet, and golangci-lint"
	@echo "  secure        - Run gosec and govulncheck security audits"
	@echo "  clean         - Remove build artifacts, venv, and coverage files"
	@echo "  docker        - Build hardened distroless container"

