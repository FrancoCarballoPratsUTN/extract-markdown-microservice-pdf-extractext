# syntax=docker/dockerfile:1

ARG PDF_OXIDE_VERSION=v0.3.78

FROM golang:1.26.10-bookworm AS oxide

ARG PDF_OXIDE_VERSION

RUN go run github.com/yfedoseev/pdf_oxide/go/cmd/install@${PDF_OXIDE_VERSION} \
        -shared \
        -dir /opt/pdf_oxide \
    && test -f /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so

# Mantener CGO deshabilitado en el entorno de la imagen para que el target de
# test herede la misma configuración de build puro-Go que el binario de
# producción.
FROM golang:1.26.10-bookworm AS builder

ENV CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM builder AS test

# pdf_oxide se carga en tiempo de ejecución mediante purego; copiar la librería
# nativa en la imagen de test para que los tests de integración usen el mismo
# motor que producción.
COPY --from=oxide /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so /opt/pdf_oxide/libpdf_oxide.so

ENV PDF_OXIDE_LIB_PATH=/opt/pdf_oxide/libpdf_oxide.so

# Validar la imagen en CI con: docker build --target test .
RUN go vet ./... \
    && go test ./... \
    && go test -tags unit ./... \
    && go test -tags integration -timeout 5m ./internal/infrastructure/pdf/

# Prueba: Chainguard glibc-dynamic (Wolfi) para reducir CVEs de la base Debian.
# Mantiene glibc (necesario para libpdf_oxide.so), sin shell/pkg manager, UID 65532.
FROM cgr.dev/chainguard/glibc-dynamic@sha256:aab5cd0b4cc5edd9c87b6e7aa9ddaa3fb6ffcec559b9e5473c55bbc937b65399 AS runtime

# El runtime necesita la librería nativa de pdf_oxide porque purego la carga
# con dlopen después de que el servidor arranque. glibc-dynamic ya incluye libgcc.
COPY --from=oxide /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so /opt/pdf_oxide/libpdf_oxide.so
COPY --from=builder /out/server /app/server

ENV PDF_OXIDE_LIB_PATH=/opt/pdf_oxide/libpdf_oxide.so

# Óptimo medido para la cuota del despliegue (cpus: 1.0 por réplica). Se fija
# también en la imagen para que `docker run` sin `-e` use la misma configuración
# que el compose y el binario local.
ENV MAX_WORKERS=2 \
    QUEUE_SIZE=56

WORKDIR /app
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/server"]
