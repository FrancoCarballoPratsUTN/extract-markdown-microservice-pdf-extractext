# extract-markdown-microservice-pdf-extractext

Microservicio de alta concurrencia escrito en Go para extraer archivos PDF y
devolverlos en Markdown. Utiliza una arquitectura de tres capas, una cola
de trabajadores acotada, errores RFC 9457 y configuración compatible con
12-Factor. La extracción corre en proceso con el motor `pdf_oxide`
(binding Go vía purego, sin CGO ni procesos extra).

## Punto de acceso

`POST /extract` acepta `multipart/form-data` con el campo `file` o un cuerpo
binario `application/pdf` / `application/octet-stream`.

```json
{
  "content": "# Guía Scrum\n\nKen Schwaber & Jeff Sutherland\n...",
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
| 413 | El cuerpo supera `MAX_UPLOAD_MB` o el documento supera `MAX_PAGES` |
| 503 | El control de admisión está saturado |
| 500 | Fallo de la extracción |

## Inicio rápido

El contenedor incluye la biblioteca `libpdf_oxide` y no requiere instalar
dependencias en el equipo anfitrión. El siguiente comando inicia cinco réplicas
detrás de un punto de acceso local:

```bash
docker compose up --build
docker run --rm -p 8080:8080 pdf-extract-md

curl -X POST --data-binary @tests/stress/pdfs/2020-Scrum-Guide-Spanish-Latin-South-American.pdf \
  -H "Content-Type: application/pdf" http://127.0.0.1:8080/extract
```

## CVE-2025-60876 y el healthcheck

La imagen runtime usa `gcr.io/distroless/base-debian13:nonroot`, fijada al
digest `sha256:9039a88709bcc2bacf5a2faac331d65dd6840f763f225ad9594cc0c67bcd0fba`.
No incluye `wget` ni un `HEALTHCHECK` interno. Alpine 3.24.3 fue descartada
porque `pdf_oxide` requiere símbolos `glibc` hasta `GLIBC_2.35` y `gcompat`
no permite cargar la biblioteca nativa. Las sondas externas pueden consultar
`/healthz` y el proceso mantiene `USER 65532:65532`.

Para usar una instancia existente de Traefik:

```bash
docker compose -f docker-compose.yml -f docker-compose.traefik.yml up --build
```

La justificación de la arquitectura, las mediciones y el análisis comparativo
están en **[INFORME.md](INFORME.md)**.

## Dependencia pdf_oxide

La extracción la hace `pdf_oxide` (core Rust, binding oficial de Go
`github.com/yfedoseev/pdf_oxide/go`), cargado en tiempo de ejecución con
`purego` (`dlopen`), por lo que el binario se compila con `CGO_ENABLED=0` y
no enlaza la biblioteca al build:

- La imagen la instala con el installer pineado
  (`go run github.com/yfedoseev/pdf_oxide/go/cmd/install@v0.3.78 -shared`) en
  `/opt/pdf_oxide/libpdf_oxide.so`.
- `PDF_OXIDE_LIB_PATH` apunta a esa biblioteca. El servidor valida en el
  arranque que cargue y aborta con un error claro si no (probe de arranque),
  en vez de fallar `500` en cada petición.
- El motor corre en el mismo proceso que el servidor HTTP. Un PDF malformado
  se traduce en un error controlado (400/500) vía mapeo de sentinels y
  `recover()` en el adaptador; no hay proceso worker que reemplazar.

Para desarrollo local sin Docker, la misma instalación sirve:

```bash
go run github.com/yfedoseev/pdf_oxide/go/cmd/install@v0.3.78 -shared
export PDF_OXIDE_LIB_PATH=$HOME/.cache/pdf_oxide/v0.3.78/lib/linux_amd64/libpdf_oxide.so
go test -tags integration ./...
```

## Configuración

| Variable | Predeterminado | Propósito |
| --- | --- | --- |
| `PORT` | `8080` | Puerto de escucha |
| `MAX_UPLOAD_MB` | `50` | Tamaño máximo del cuerpo |
| `MAX_PAGES` | `1000` | Tope de páginas por documento (`0` = sin límite) |
| `MAX_WORKERS` | `2` | Extracciones concurrentes por réplica |
| `QUEUE_SIZE` | `56` | Capacidad de la cola por réplica |
| `QUEUE_MAX_MB` | `256` | Límite de bytes de trabajos activos y pendientes |
| `READ_TIMEOUT` / `WRITE_TIMEOUT` | `30` | Tiempo de espera en segundos |
| `SHUTDOWN_TIMEOUT` | `35` | Deadline (segundos) para dejar de aceptar y drenar la cola al apagar |
| `LOG_LEVEL` | `info` | Nivel mínimo: `debug`, `info`, `warn` o `error` |
| `LOG_FORMAT` | `json` | Formato de los logs: `json` o `text` |
| `PDF_OXIDE_LIB_PATH` | — | Ruta de `libpdf_oxide.so` (obligatoria) |
| `CONSUL_ENABLED` | `false` | Habilita el registro y la carga de configuración desde Consul |
| `CONSUL_HTTP_ADDR` | — | URL HTTP del agente Consul |
| `CONSUL_HTTP_TOKEN` | — | Token del agente Consul |
| `CONSUL_HTTP_CA_FILE` | — | CA PEM opcional para validar Consul por TLS |
| `CONSUL_KV_PREFIX` | — | Prefijo KV de configuración, obligatorio con Consul habilitado |
| `CONSUL_SERVICE_ADDRESS` | hostname | Dirección anunciada del servicio |
| `CONSUL_SERVICE_NAME` | `extract` | Nombre del servicio registrado |
| `CONSUL_SERVICE_ID` | `extract-<address>` | ID del registro |
| `CONSUL_SERVICE_CHECK_URL` | `http://<address>:<port>/healthz` | URL del check HTTP |
| `CONSUL_SERVICE_TAGS` | — | Tags separados por `;`; las comas forman parte del tag |

Los defaults de la tabla son los del archivo Compose (despliegue recomendado);
si una variable no llega, `internal/config` aplica sus propios defaults
(`MAX_WORKERS=10`, `QUEUE_SIZE=16`, el resto igual) y aborta el arranque ante un
valor inválido. `LOG_LEVEL`/`LOG_FORMAT` los lee `internal/logging` y también son
*fail-fast*: un valor desconocido impide el arranque.

`MAX_WORKERS` se ajusta a la cuota de CPU de cada réplica. Con la configuración
del archivo Compose, dos trabajadores superan a uno y cuatro en rendimiento.
La admisión máxima por réplica es `MAX_WORKERS + QUEUE_SIZE`.

Cuando `CONSUL_ENABLED=true`, `CONSUL_HTTP_ADDR`, `CONSUL_HTTP_TOKEN` y
`CONSUL_KV_PREFIX` son obligatorios. `CONSUL_SERVICE_TAGS` usa `;` como único
separador: por ejemplo,
`security-headers@file,extract-cb@file;metrics` produce dos tags, y la coma
del primer tag se conserva. Si un entorno usaba comas para separar tags, debe
migrar a `;` y revisar el log de arranque `"registered in Consul"` con el
atributo `tags` para confirmar los tags efectivos.

## Logging

El servicio emite logs estructurados a `stdout`. La observabilidad es
**sólo logging**: no hay métricas, tracing ni endpoint de telemetría.

Las requests pueden incluir `X-Request-ID`. Los valores válidos se conservan
en la respuesta y se propagan a los logs de acceso, extracción, rechazo y
recovery. Si el header falta o es inválido, el servicio genera un identificador
aleatorio opaco de 26 caracteres base32 y lo devuelve en la respuesta.

- **Access log** (`"http request"`): una línea por petición con `method`, `path`,
  `status` y `duration_ms`. El nivel sigue
  al status: 5xx ⇒ `ERROR`, 4xx ⇒ `WARN`, el resto ⇒ `INFO`. Las sondas
  `/healthz` no se registran.
- **Pipeline**: la cola emite `"extraction completed"` (`pages`, `duration_ms`)
  o `"extraction failed"`, `"queue full; rejecting job"` cuando
  se satura y `"extraction abandoned before completion"` cuando el cliente
  abandona la espera.
- **Arranque/apagado**: `"application starting"` con la configuración efectiva,
  `"server listening"`, `"shutting down"`, `"draining worker pool"`,
  `"worker pool drained"`. Un error de apagado (`"application stopped"`) sale
  con código ≠ 0.
- Un `panic` en el extractor o en un handler se recupera, se registra con
  `stack` y se responde `500`. La petición que lo provocó queda además en el
  access log, con su `500`.
- Los logs **nunca** incluyen el cuerpo del PDF ni rutas de fichero. Los
  valores que vienen de fuera se acotan en longitud antes de escribirse: el
  `path` y el `method` de la petición, el valor
  de un `panic` (256 caracteres) y su `stack` (4096).

En local se puede forzar el detalle: `LOG_LEVEL=debug LOG_FORMAT=text make run`.

## Organización del código

```text
cmd/server                 composición de configuración, HTTP y apagado
internal/domain            entidades y errores del dominio
internal/usecase           reglas de negocio y cola de trabajadores
internal/infrastructure/pdf adaptador de pdf_oxide
internal/presentation/http  controladores, validación y control de admisión
```

## Pruebas

La etapa `test` ejecuta análisis estático, pruebas unitarias y pruebas de
integración contra la librería real de pdf_oxide:

```bash
docker build --target test .
```

Localmente:

```bash
make test              # go vet + go test (CGO_ENABLED=0)
make test-unit         # tests con tag //go:build unit
make test-integration  # requiere PDF_OXIDE_LIB_PATH (librería real)
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

Con cinco réplicas (`cpus: 1.0`), `MAX_WORKERS=2` y `QUEUE_SIZE=56`, el perfil
de k6 (100 VUs) midió ~15 req/s con **0,00 % de errores** y la carga fija de
Vegeta (50 req/s) sirvió el **100 % de las peticiones** con p50 ~2,4 s. La
extracción con `pdf_oxide` cuesta ~4× más CPU por petición que la versión
anterior con PDFium (~230 ms contra ~52 ms), así que con la cuota total de
5 CPUs el techo de throughput queda en ~15–20 req/s: CPU-bound, no de cola ni
de transporte. El informe técnico contiene las tablas completas, las condiciones
de medición y el análisis del cuello de botella.
