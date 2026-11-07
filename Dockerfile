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

FROM gcr.io/distroless/base-debian13@sha256:9039a88709bcc2bacf5a2faac331d65dd6840f763f225ad9594cc0c67bcd0fba AS runtime

# El runtime necesita la librería nativa de pdf_oxide porque purego la carga
# con dlopen después de que el servidor arranque.
COPY --from=oxide /opt/pdf_oxide/lib/linux_amd64/libpdf_oxide.so /opt/pdf_oxide/libpdf_oxide.so
# base-debian13 no garantiza el usuario nonroot incluido en la imagen cc; copiar
# la dependencia del loader explícitamente y fijar el UID/GID efectivo abajo.
COPY --from=builder /usr/lib/x86_64-linux-gnu/libgcc_s.so.1 /usr/lib/x86_64-linux-gnu/libgcc_s.so.1
COPY --from=builder /out/server /app/server

ENV PDF_OXIDE_LIB_PATH=/opt/pdf_oxide/libpdf_oxide.so

WORKDIR /app
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/server"]
