# syntax=docker/dockerfile:1

# pdf_oxide version pinned to match go.mod. Bumping it requires re-verifying the
# integration tests and the installer output path below.
ARG PDF_OXIDE_VERSION=v0.3.78

# ---------------------------------------------------------------------------
# pdf_oxide native library.
#
# Kept as its own stage so the runtime and the test target get only the shared
# library they dlopen via PDF_OXIDE_LIB_PATH (purego, CGO_ENABLED=0).
# ---------------------------------------------------------------------------
FROM golang:1.26-bookworm AS oxide

ARG PDF_OXIDE_VERSION

RUN go run github.com/yfedoseev/pdf_oxide/go/cmd/install@${PDF_OXIDE_VERSION} \
        -shared \
        -dir /opt/pdf_oxide \
    && test -f /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so

# Flat, immutable path so every stage and the runtime share one env value.
# ---------------------------------------------------------------------------
# Build the server.
#
# CGO_ENABLED=0: the engine is loaded at run time with purego, so the Go binary
# is fully static and needs no C toolchain in the build image.
# ---------------------------------------------------------------------------
FROM golang:1.26-bookworm AS builder

# CGO_ENABLED must be a build-time constant, not just a flag on one RUN: the
# test stage inherits it and with cgo enabled pdfoxide compiles its cgo binding
# (which needs a C toolchain and the static library this image does not have).
ENV CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---------------------------------------------------------------------------
# Test target. Carries the real pdf_oxide library so the integration tests
# exercise the same code path the runtime image does.
#
#   docker build --target test .
# ---------------------------------------------------------------------------
FROM builder AS test

COPY --from=oxide /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so /opt/pdf_oxide/libpdf_oxide.so

ENV PDF_OXIDE_LIB_PATH=/opt/pdf_oxide/libpdf_oxide.so

RUN go vet ./... \
    && go test ./... \
    && go test -tags unit ./... \
    && go test -tags integration -timeout 5m ./internal/infrastructure/pdf/

# ---------------------------------------------------------------------------
# Runtime: server + libpdf_oxide.so, nothing else.
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        libgcc-s1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 1000 appuser

COPY --from=oxide /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so /opt/pdf_oxide/libpdf_oxide.so
COPY --from=builder /out/server /app/

ENV PDF_OXIDE_LIB_PATH=/opt/pdf_oxide/libpdf_oxide.so

WORKDIR /app
USER appuser
EXPOSE 8080
CMD ["/app/server"]