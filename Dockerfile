# ==============================================================================
# Build Stage: Secure Multi-Stage Go Builder
# ==============================================================================
FROM golang:1.27.3-alpine AS builder

# Install ca-certificates and git for secure Go module retrieval
RUN apk --no-cache add ca-certificates git

WORKDIR /src

# Leverage Docker cache by copying module definitions first
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy remaining source code
# internal/ holds module-private helpers; it is copied so the image always builds
# against the complete module even when production code grows an internal dependency.
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY internal/ internal/

# Compile hardened, statically-linked binary:
# - CGO_ENABLED=0: Statically linked binary without glibc dependencies
# - -trimpath: Strip host file paths from executable to prevent info leakage
# - -ldflags="-s -w -extldflags '-static'": Strip symbol tables & debug information
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -ldflags="-s -w -extldflags '-static'" \
    -o /out/tfastdiff ./cmd/tfastdiff

# Ensure strict binary execution permissions
RUN chmod 0755 /out/tfastdiff

# ==============================================================================
# Final Stage: Hardened, Minimal Non-Root Distroless Image
# ==============================================================================
# Distroless static-debian12 contains:
# - No package manager (apt/dpkg)
# - No shell (/bin/sh, /bin/bash)
# - No standard OS utilities (curl, wget, etc.)
# - Pre-installed CA certificates and tzdata
# - Pre-configured non-root user (UID 65532 / GID 65532)
FROM gcr.io/distroless/static-debian12:nonroot

# Open Containers Initiative (OCI) image metadata labels
LABEL org.opencontainers.image.title="terraform-ast-diff" \
    org.opencontainers.image.description="AST-level semantic difference engine & wrapper compatibility auditor for Terraform modules" \
    org.opencontainers.image.version="0.1.0" \
    org.opencontainers.image.vendor="23skdu" \
    org.opencontainers.image.source="https://github.com/23skdu/terraform_ast" \
    org.opencontainers.image.licenses="Unlicense"

# Working directory for module analysis
WORKDIR /workspace

# Copy statically linked binary from builder stage
COPY --from=builder /out/tfastdiff /usr/local/bin/tfastdiff

# Enforce non-root execution (UID:GID 65532:65532)
USER 65532:65532

# CLI entrypoint
ENTRYPOINT ["/usr/local/bin/tfastdiff"]
