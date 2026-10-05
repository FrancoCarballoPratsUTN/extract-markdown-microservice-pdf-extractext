# syntax=docker/dockerfile:1

# PDFium build that the go-pdfium v1.21.x test matrix runs against. Bumping this
# requires re-verifying the integration tests.
ARG PDFIUM_VERSION=8057

# ---------------------------------------------------------------------------
# PDFium native library.
#
# Kept as its own stage so the builder gets headers plus the shared library for
# cgo, and the runtime image gets only the .so it loads at execution time.
# ---------------------------------------------------------------------------
FROM golang:1.26-bookworm AS pdfium

ARG PDFIUM_VERSION

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
    && rm -rf /var/lib/apt/lists/*

# Libs and Cflags carry literal paths, not ${libdir}-style references: Go parses
# pkg-config output as shell flags and rejects an unquoted '$'.
RUN set -eux; \
    mkdir -p /opt/pdfium; \
    curl -fsSL "https://github.com/bblanchon/pdfium-binaries/releases/download/chromium%2F${PDFIUM_VERSION}/pdfium-linux-x64.tgz" -o /tmp/pdfium.tgz; \
    tar -C /opt/pdfium -xzf /tmp/pdfium.tgz; \
    rm -f /tmp/pdfium.tgz; \
    printf '%s\n' \
        'prefix=/opt/pdfium' \
        'libdir=/opt/pdfium/lib' \
        'includedir=/opt/pdfium/include' \
        '' \
        'Name: PDFium' \
        'Description: PDFium' \
        "Version: ${PDFIUM_VERSION}" \
        'Requires:' \
        '' \
        'Libs: -L/opt/pdfium/lib -lpdfium' \
        'Cflags: -I/opt/pdfium/include' \
        > /opt/pdfium/pdfium.pc; \
    test -f /opt/pdfium/lib/libpdfium.so; \
    test -f /opt/pdfium/include/fpdfview.h

# Every stage derived from this one needs both variables: pkg-config resolves the
# cgo flags at link time, the dynamic loader resolves the library at run time.
ENV PKG_CONFIG_PATH=/opt/pdfium \
    LD_LIBRARY_PATH=/opt/pdfium/lib

# ---------------------------------------------------------------------------
# Build the server and the PDFium worker.
#
# Two binaries, not one: the server speaks go-plugin to worker processes so a
# PDFium crash cannot take down the HTTP runtime.
# ---------------------------------------------------------------------------
FROM pdfium AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# cgo cannot cross-compile, so this image is linux/amd64 only.
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/pdfium-worker ./cmd/pdfium-worker

# ---------------------------------------------------------------------------
# Test target. Carries the real PDFium library so the integration tests exercise
# the same code path the runtime image does.
#
#   docker build --target test .
# ---------------------------------------------------------------------------
FROM builder AS test

ENV PDFIUM_WORKER_PATH=/out/pdfium-worker

RUN go vet ./...
RUN go test ./...
RUN go test -tags integration -v -timeout 5m ./internal/infrastructure/pdf/

# ---------------------------------------------------------------------------
# Runtime: server + worker + libpdfium.so, nothing else.
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 1000 appuser

COPY --from=pdfium /opt/pdfium/lib/libpdfium.so /opt/pdfium/lib/
COPY --from=builder /out/server /out/pdfium-worker /app/

ENV LD_LIBRARY_PATH=/opt/pdfium/lib

WORKDIR /app
USER appuser
EXPOSE 8080
CMD ["/app/server"]