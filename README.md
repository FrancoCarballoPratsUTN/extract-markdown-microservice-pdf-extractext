# extract-markdown-microservice-pdf-extractext

Microservicio de alta concurrencia escrito en Go para extraer texto de archivos
PDF y devolverlo en Markdown. Utiliza una arquitectura de tres capas, una cola
de trabajadores acotada, errores RFC 9457 y configuración compatible con
12-Factor.

## Punto de acceso

`POST /extract` acepta `multipart/form-data` con el campo `file` o un cuerpo
binario `application/pdf` / `application/octet-stream`.

```json
{
  "content": "Ken Schwaber & Jeff Sutherland\n...",
  "page_count": 16
}
```

Los errores usan `application/problem+json` (RFC 9457):

```json
{ "type": "about:blank", "title": "Bad Request", "status": 400, "detail": "invalid pdf" }
```

| Estado | Significado |
| --- | --- |
| 400 | Cuerpo vacío, PDF corrupto o tipo de contenido no admitido |
| 413 | El cuerpo supera `MAX_UPLOAD_MB` |
| 503 | El control de admisión está saturado |
| 500 | Fallo del trabajador de PDFium |

## Inicio rápido

El contenedor incluye PDFium y no requiere instalar dependencias en el equipo
anfitrión. El siguiente comando inicia cinco réplicas detrás de un punto de
acceso local:

```bash
docker compose up --build
docker run --rm -p 8080:8080 pdf-extract-md

curl -X POST --data-binary @tests/stress/pdfs/2020-Scrum-Guide-Spanish-Latin-South-American.pdf \
  -H "Content-Type: application/pdf" http://127.0.0.1:8080/extract
```

Para usar una instancia existente de Traefik:

```bash
docker compose -f docker-compose.yml -f docker-compose.traefik.yml up --build
```

La justificación de la arquitectura, las mediciones y el análisis comparativo
están en **[INFORME.md](INFORME.md)**.

## Dependencia PDFium

PDFium es una biblioteca de C++ y no se enlaza directamente con el servidor.
La imagen contiene dos binarios:

- `/app/server`: servidor HTTP que se comunica con los trabajadores mediante
  `go-plugin`.
- `/app/pdfium-worker`: proceso PDFium iniciado por la cola de trabajadores.

Esta separación limita el impacto de un fallo nativo a un solo trabajador, que
la cola descarta y reemplaza. El inicio valida que el binario del trabajador
exista y pueda iniciarse; una configuración inválida detiene el arranque en vez
de devolver errores `500` para todas las solicitudes.

La imagen usa Go 1.26, PDFium `chromium/8057` para Linux x64 y Debian bookworm.
Para probar otra versión:

```bash
docker build --target test --build-arg PDFIUM_VERSION=7988 .
```

## Configuración

| Variable | Predeterminado | Propósito |
| --- | --- | --- |
| `PORT` | `8080` | Puerto de escucha |
| `MAX_UPLOAD_MB` | `50` | Tamaño máximo del cuerpo |
| `MAX_WORKERS` | `2` | Cantidad de procesos PDFium |
| `QUEUE_SIZE` | `56` | Capacidad de la cola por réplica |
| `QUEUE_MAX_MB` | `256` | Límite de bytes de trabajos activos y pendientes |
| `READ_TIMEOUT` / `WRITE_TIMEOUT` | `30` | Tiempo de espera en segundos |
| `PDFIUM_WORKER_PATH` | Junto a `server` | Ubicación del binario trabajador |

`MAX_WORKERS` se ajusta a la cuota de CPU de cada réplica. Con la configuración
del archivo Compose, dos trabajadores superan a uno y cuatro en rendimiento.
La admisión máxima por réplica es `MAX_WORKERS + QUEUE_SIZE`.

## Organización del código

```text
cmd/server                 composición de configuración, HTTP y apagado
cmd/pdfium-worker          proceso de PDFium iniciado por la cola
internal/domain            entidades y errores del dominio
internal/usecase           reglas de negocio y cola de trabajadores
internal/infrastructure/pdf adaptador de PDFium
internal/presentation/http  controladores, validación y control de admisión
```

## Pruebas

La etapa `test` ejecuta análisis estático, pruebas unitarias y pruebas de
integración contra PDFium:

```bash
docker build --target test .
```

Para una ejecución local de las pruebas de integración:

```bash
export PKG_CONFIG_PATH=/opt/pdfium LD_LIBRARY_PATH=/opt/pdfium/lib
go build -o bin/pdfium-worker ./cmd/pdfium-worker
PDFIUM_WORKER_PATH=$PWD/bin/pdfium-worker go test -tags integration ./...
```

## Pruebas de carga

Se necesitan un servicio en ejecución y `k6 v2.3.0` o `vegeta v12.13.0`:

```bash
k6 run scripts/k6/spike.js
./scripts/vegeta/attack.sh
```

Los scripts usan los cuatro PDF de `tests/stress/pdfs/` y permiten configurar
`TARGET`, `CORPUS`, `RATE`, `DURATION` y `TIMEOUT`. Por defecto apuntan a
`http://127.0.0.1:8080`. Con el Traefik compartido, el destino es
`https://extract.universidad.localhost`; el certificado de mkcert puede
requerir configurar `SSL_CERT_FILE` o el modo inseguro de la herramienta.

## Resultados medidos

Con cinco réplicas, `MAX_WORKERS=2` y `QUEUE_SIZE=56`, las ejecuciones de k6
alcanzaron aproximadamente `31 req/s`, con una mejora del p90 y p95 frente a la
configuración de referencia. El informe técnico contiene las tablas completas,
las condiciones de medición y el análisis del cuello de botella.
