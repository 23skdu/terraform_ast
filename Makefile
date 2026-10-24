.PHONY: all build test verify fuzz lint clean docker secure accuracy-gate help

BIN := bin/tfastdiff
FUZZ_TIME ?= 10

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

lint:
	gofmt -l .
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout 10m ./...; \
	fi

clean:
	rm -rf bin coverage *.out *.prof *.test cover.html

docker:
	docker build -t tfastdiff:latest .

secure:
	gosec -fmt=sarif ./...
	govulncheck ./...

accuracy-gate: build
	bash scripts/validate_upgrade_accuracy.sh

help:
	@echo "terraform_ast build targets:"
	@echo "  build         - Compile bin/tfastdiff"
	@echo "  test          - Run unit tests with coverage"
	@echo "  verify        - Run full verification suite (fmt, vet, lint, 100% coverage, fuzz)"
	@echo "  accuracy-gate - Run upstream EKS/GKE upgrade path accuracy validation & confidence scoring"
	@echo "  fuzz          - Run extended fuzz smoke suite"
	@echo "  lint          - Run gofmt, go vet, and golangci-lint"
	@echo "  secure        - Run gosec and govulncheck security audits"
	@echo "  clean         - Remove build artifacts and coverage files"
	@echo "  docker        - Build hardened distroless container"
