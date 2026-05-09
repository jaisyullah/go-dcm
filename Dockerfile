# ============================================================
# Stage 1: Build the Go binary
# ============================================================
FROM golang:1.26-trixie AS builder

WORKDIR /app

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X go-dcm/handler.AppVersion=$(git describe --tags --always --dirty 2>/dev/null || echo 'docker')" \
    -o /go-dcm .

# ============================================================
# Stage 2: Minimal runtime image
# ============================================================
FROM debian:trixie-slim

# Install DCMTK and required runtime dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        dcmtk \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd -r godcm && useradd -r -g godcm -d /app -s /sbin/nologin godcm

WORKDIR /app

# Copy binary from builder
COPY --from=builder /go-dcm .

# Create temp directory for conversions
RUN mkdir -p /tmp/dcm && chown godcm:godcm /tmp/dcm

# Switch to non-root user
USER godcm

# Environment defaults
ENV PORT=8080
ENV MAX_IMAGE_UPLOAD_MB=100
ENV MAX_PDF_UPLOAD_MB=200
ENV MAX_CDA_UPLOAD_MB=100
ENV MAX_STL_UPLOAD_MB=200

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/app/go-dcm", "-health"] || curl -f http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/go-dcm"]
