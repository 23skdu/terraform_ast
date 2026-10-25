# Contributing to terraform_ast

Thank you for your interest in contributing to `terraform_ast`!

## Code Quality Standards & Gates

Every contribution must pass the strict verification pipeline before being merged:

1. **100.0% Statement Coverage**: Every package and function must maintain 100.0% statement coverage. Partial coverage or untested branches fail the CI gate.
2. **Formatting & Static Analysis**: Code must be formatted with `gofmt`, pass `go vet ./...`, and produce zero issues in `golangci-lint run --timeout 10m ./...` (including `govet`, `errcheck`, `staticcheck`, `unused`, and `gosec`).
3. **Fuzz Invariant Testing**: All native Go fuzz targets must pass smoke tests without panics or invariant violations.
4. **Style Guidelines**: Maintain ASCII headers and documentation without emoji. Use standard GitHub Flavored Markdown with clickable file references.

## Local Development Workflow

Run the full verification suite before submitting pull requests:

```bash
# Run verification with default 10s fuzz smoke test
make verify

# Or specify a custom fuzz time
bash scripts/verify.sh 2

# Clean build and test artifacts
make clean
```

## Pull Request Checklist

- [ ] `make verify` passes cleanly with 100.0% statement coverage across all packages.
- [ ] No regression in Golden Test baselines (`test/golden/`).
- [ ] Any new CLI flags or features are documented in `README.md` and `docs/cli_usage.md`.
- [ ] Unit and fuzz tests cover all new branches, edge cases, and error conditions.
