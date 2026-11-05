# syntax=docker/dockerfile:1

ARG PDF_OXIDE_VERSION=v0.3.78

FROM golang:1.26.10-bookworm AS oxide

ARG PDF_OXIDE_VERSION

RUN go run github.com/yfedoseev/pdf_oxide/go/cmd/install@${PDF_OXIDE_VERSION} \
        -shared \
        -dir /opt/pdf_oxide \
    && test -f /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so

# Keep CGO disabled in the image environment so the test target inherits the
# same pure-Go build configuration as the production binary.
FROM golang:1.26.10-bookworm AS builder

ENV CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM builder AS test

# pdf_oxide is loaded at runtime via purego; copy the native library into the
# test image so integration tests exercise the same engine as production.
COPY --from=oxide /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so /opt/pdf_oxide/libpdf_oxide.so

ENV PDF_OXIDE_LIB_PATH=/opt/pdf_oxide/libpdf_oxide.so

# Gate the image in CI with: docker build --target test .
RUN go vet ./... \
    && go test ./... \
    && go test -tags unit ./... \
    && go test -tags integration -timeout 5m ./internal/infrastructure/pdf/

FROM gcr.io/distroless/base-debian13@sha256:9039a88709bcc2bacf5a2faac331d65dd6840f763f225ad9594cc0c67bcd0fba AS runtime

# The runtime needs the native pdf_oxide library because purego loads it with
# dlopen after the server starts.
COPY --from=oxide /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so /opt/pdf_oxide/libpdf_oxide.so
# base-debian13 does not guarantee the cc image's bundled nonroot user; copy
# the loader dependency explicitly and set the effective UID/GID below.
COPY --from=builder /usr/lib/x86_64-linux-gnu/libgcc_s.so.1 /usr/lib/x86_64-linux-gnu/libgcc_s.so.1
COPY --from=builder /out/server /app/server

ENV PDF_OXIDE_LIB_PATH=/opt/pdf_oxide/libpdf_oxide.so

WORKDIR /app
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/server"]
