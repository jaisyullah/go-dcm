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
    -ldflags="-s -w -X dicom-converter-api/handler.AppVersion=$(git describe --tags --always --dirty 2>/dev/null || echo 'docker')" \
    -o /dicom-converter-api .

# ============================================================
# Stage 2: Minimal runtime image
# ============================================================
FROM debian:trixie-slim

# Install DCMTK and required runtime dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        dcmtk \
        ca-certificates \
        curl \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd -r dicomconv && useradd -r -g dicomconv -d /app -s /sbin/nologin dicomconv

WORKDIR /app

# Copy binary from builder
COPY --from=builder /dicom-converter-api .

# Create temp directory for conversions
RUN mkdir -p /tmp/dcm && chown dicomconv:dicomconv /tmp/dcm

# Switch to non-root user
USER dicomconv

# Environment defaults
ENV PORT=8080
ENV MAX_IMAGE_UPLOAD_MB=100
ENV MAX_PDF_UPLOAD_MB=200
ENV MAX_CDA_UPLOAD_MB=100
ENV MAX_STL_UPLOAD_MB=200

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["curl", "-f", "http://localhost:8080/health"]

ENTRYPOINT ["/app/dicom-converter-api"]
