# extract-markdown-microservice-pdf-extractext

Microservicio de alta concurrencia escrito en Go para extraer archivos PDF y
devolverlos en Markdown. Utiliza una arquitectura de tres capas (presentación,
casos de uso e infraestructura) alrededor de un dominio sin dependencias, una
cola de trabajadores acotada, errores RFC 9457 y configuración compatible con
12-Factor. La extracción corre en proceso con el motor `pdf_oxide`
(binding Go vía purego, sin CGO ni procesos extra).

## Punto de acceso

- `GET /healthz` devuelve `200` con el cuerpo `ok`. Es una sonda de vida
  estática: no consulta dependencias y queda fuera de la admisión y del
  deadline, así que responde aunque el servicio esté saturado.
- `POST /extract` acepta `multipart/form-data` con el campo `file` o un cuerpo
  binario `application/pdf` / `application/octet-stream`.

Respuesta correcta (`200`, `application/json`):

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
| 404 | Ruta desconocida (`application/problem+json`) |
| 405 | `/extract` llamado con un método distinto de `POST` |
| 413 | El PDF supera `MAX_UPLOAD_MB`, el sobre multipart excede `MAX_UPLOAD_MB` + 8 KiB o el documento supera `MAX_PAGES` |
| 500 | Fallo de la extracción |
| 503 | El control de admisión o la cola están saturados |

## Inicio rápido

El contenedor incluye `libpdf_oxide`, así que en Docker no hay que instalar
dependencias en el equipo anfitrión. La vía recomendada es Docker Compose; más
abajo se explican también [un único contenedor](#un-único-contenedor-sin-traefik)
y el [binario local](#binario-local-sin-docker), y el despliegue
[tras el Traefik compartido](#7-con-el-traefik-compartido).

### 1. Requisitos

- Docker Engine 24+ con Compose v2.24+ (`docker compose version`); el
  `env_file` opcional usa el campo `required`.
- Sólo para ejecutar sin Docker: Go 1.26+ (el `Dockerfile` usa 1.26.10) y
  `make`.
- Los ejemplos usan los PDF de `tests/stress/pdfs/`, que ya están en el repo.

### 2. Configuración (opcional)

```bash
cp .env.example .env
```

`.env` está en `.gitignore`, así que no viene en el clon. Sin él, Compose
arranca con los valores predeterminados de la sección
[Configuración](#configuración); con él se ajustan los límites y timeouts. En
Docker no hay que tocar `PDF_OXIDE_LIB_PATH`: la imagen fija la ruta de su
propia biblioteca.

### 3. Levantar con Docker Compose (recomendado)

```bash
docker compose up --build
```

Compila la imagen, levanta cinco réplicas y un Traefik local que publica el
servicio en `127.0.0.1:8080`. El comando queda en primer plano mostrando los
logs; para ejecutarlo en segundo plano usar `docker compose up -d --build` y
consultar los logs con `docker compose logs -f extract`.

Esperar a que el servicio responda antes de enviar PDFs:

```bash
until curl -fsS http://127.0.0.1:8080/healthz >/dev/null; do sleep 1; done
```

Si no responde, revisar `docker compose logs extract` (por ejemplo, un puerto
8080 ya ocupado impide que arranque el Traefik del compose).

### 4. Comprobar el servicio y extraer un PDF

```bash
curl -i http://127.0.0.1:8080/healthz

curl -X POST \
  --data-binary @tests/stress/pdfs/2020-Scrum-Guide-Spanish-Latin-South-American.pdf \
  -H "Content-Type: application/pdf" \
  http://127.0.0.1:8080/extract

# Alternativa multipart: el campo debe llamarse "file".
curl -X POST \
  -F "file=@tests/stress/pdfs/2020-Scrum-Guide-Spanish-Latin-South-American.pdf;type=application/pdf" \
  http://127.0.0.1:8080/extract
```

### 5. Apagar

```bash
docker compose down
```

### 6. Alternativas de ejecución

#### Un único contenedor (sin Traefik)

```bash
docker build -t pdf-extract-md .
docker run --rm -p 127.0.0.1:8080:8080 pdf-extract-md
```

La imagen es autónoma (incluye la biblioteca) y arranca con los valores
predeterminados. No ejecutarla a la vez que Compose: ambos publican el puerto
8080. Los límites se pasan con `-e`, por ejemplo
`-e MAX_WORKERS=2 -e QUEUE_SIZE=56`.

#### Binario local (sin Docker)

```bash
make install
export PDF_OXIDE_LIB_PATH="$HOME/.cache/pdf_oxide/v0.3.78/lib/linux_amd64/libpdf_oxide.so"
make run
```

`make install` instala `libpdf_oxide` v0.3.78 en la caché del usuario (es
idempotente). Si la ruta no existe porque la arquitectura no es `linux_amd64`,
localizarla con `find "$HOME/.cache/pdf_oxide" -name libpdf_oxide.so`.
`make run` compila `bin/server` y lo ejecuta en primer plano; sin
`PDF_OXIDE_LIB_PATH` el arranque aborta con un error explícito en vez de fallar
en cada petición. Los pasos 4 y 5 siguen aplicando (el puerto por defecto es
8080; se detiene con `Ctrl+C`, que drena la cola). El archivo `.env` lo lee
Compose, no `make run`: para cambiar la configuración del binario, pasar las
variables en la línea de comandos, por ejemplo:

```bash
LOG_LEVEL=debug LOG_FORMAT=text make run
```

### 7. Con el Traefik compartido

Para usar el Traefik de `Documents/Services` (red externa `mired`, entrada
`https` con TLS para `*.universidad.localhost`) en lugar del Traefik del compose
local:

```bash
docker compose -f docker-compose.traefik.yml up --build -d
curl --cacert "$(mkcert -CAROOT)/rootCA.pem" \
  https://extract.universidad.localhost/healthz
```

`docker-compose.traefik.yml` es un compose autónomo (no un override): declara el
servicio completo con cinco réplicas, se une a la red ya existente `mired` y
publica el router por labels. No crea redes ni toca la configuración del Traefik
compartido. El healthcheck activo de Traefik consulta `/healthz` en cada réplica
para no enrutar a instancias caídas. Requiere que la red `mired` y el
certificado de mkcert ya existan (ver los requisitos en la cabecera del archivo).

## Imagen runtime, hardening y CVEs

La imagen runtime usa `cgr.dev/chainguard/glibc-dynamic` (Wolfi/glibc), fijada
por digest en el `Dockerfile` (actual:
`sha256:aab5cd0b4cc5edd9c87b6e7aa9ddaa3fb6ffcec559b9e5473c55bbc937b65399`).
Se eligió una base con glibc porque `libpdf_oxide.so` se carga por `dlopen` y
requiere símbolos glibc (Alpine/musl fue descartada: `gcompat` no permite
cargar la biblioteca). La imagen no incluye shell ni gestor de paquetes, y el
`Dockerfile` fija `USER 65532:65532`. No hay `HEALTHCHECK` interno ni `wget`;
las sondas externas/Traefik/Consul consultan `/healthz`.

**Riesgo aceptado — CVE-2026-84243 (High, `glibc 2.44-r14` en la base):** al
momento de esta nota, el scan de la imagen runtime reporta `CVE-2026-84243` en
`glibc` del paquete base (Wolfi/apk). No es un defecto del código Go del
servicio (`CGO_ENABLED=0`), pero sí afecta al proceso porque `libpdf_oxide.so`
depende de la glibc de la imagen en runtime. Se acepta mientras no exista un
digest actualizado de `glibc-dynamic` con el fix; se mitiga con hardening del
contenedor (no root, sin shell/pkg manager, `read_only`, `cap_drop: ALL`,
`no-new-privileges`) y límites de entrada. **Acción:** re-escanear al actualizar
el digest de la base y cerrar/actualizar este riesgo cuando el paquete quede
corregido. Detalle: `tasks/plan-markdown.md` (RA-1).

El `docker-compose.yml` declara el mismo contrato en el despliegue del servicio
`extract`: `read_only: true`, `cap_drop: ["ALL"]` y
`security_opt: ["no-new-privileges:true"]`. El servidor no escribe en disco ni
necesita capacidades, así que no se añade `tmpfs` (la extracción real con PDF se
verificó con el rootfs en sólo lectura). Traefik queda fuera porque monta el
socket de Docker. Se comprueba con `docker inspect` (`ReadonlyRootfs`,
`CapDrop`, `SecurityOpt`).

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

Para desarrollo local sin Docker, la instalación es la de
[Binario local (sin Docker)](#binario-local-sin-docker); los tests de
integración usan la misma biblioteca:

```bash
make install
export PDF_OXIDE_LIB_PATH="$HOME/.cache/pdf_oxide/v0.3.78/lib/linux_amd64/libpdf_oxide.so"
make test-integration
```

## Configuración

| Variable | Predeterminado | Propósito |
| --- | --- | --- |
| `PORT` | `8080` | Puerto de escucha |
| `MAX_UPLOAD_MB` | `50` | Tope del documento PDF; el sobre multipart admite hasta `MAX_UPLOAD_MB` + 8 KiB |
| `MAX_PAGES` | `1000` | Tope de páginas por documento (`0` = sin límite) |
| `MAX_WORKERS` | `10` | Extracciones concurrentes por réplica |
| `QUEUE_SIZE` | `16` | Capacidad de la cola por réplica |
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
| `CONSUL_SERVICE_ID` | `<CONSUL_SERVICE_NAME>-<address>` | ID del registro |
| `CONSUL_SERVICE_CHECK_URL` | `http://<address>:<port>/healthz` | URL del check HTTP |
| `CONSUL_SERVICE_TAGS` | — | Tags separados por `;`; las comas forman parte del tag |

Los valores de la tabla son los que aplica `internal/config` cuando la variable
no llega; coinciden con `.env.example` y con el arranque de Compose sin `.env`
(en cuanto a configuración del proceso, el compose sólo fija
`PDF_OXIDE_LIB_PATH` y el margen `STOP_GRACE_PERIOD`). Un valor inválido aborta
el arranque (fail-fast) en vez de caer a un default. `LOG_LEVEL`/`LOG_FORMAT`
los lee `internal/logging` y también son *fail-fast*.

`SHUTDOWN_TIMEOUT` es el deadline interno del servicio. `STOP_GRACE_PERIOD`
(`45s` por defecto) sólo lo usa Compose como margen de Docker: debe quedar por
encima de `SHUTDOWN_TIMEOUT` para que el proceso drene antes de que lo maten.

`MAX_UPLOAD_MB` limita el documento PDF, no el sobre de la petición: en
`multipart/form-data` el cuerpo completo puede ocupar hasta `MAX_UPLOAD_MB` +
8 KiB (boundaries y cabeceras), pero la parte `file` sigue acotada por
`MAX_UPLOAD_MB`.

`MAX_WORKERS` se ajusta a la cuota de CPU de cada réplica. Con la cuota medida
(`cpus: 1.0`), dos trabajadores superan a uno y a cuatro en rendimiento; con los
defaults de 10 trabajadores el servicio también arranca, pero retiene más
memoria y devuelve más `503`. La admisión máxima por réplica es
`MAX_WORKERS + QUEUE_SIZE`.

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

En local se puede forzar el detalle de los logs con las variables de entorno al
arrancar: `LOG_LEVEL=debug LOG_FORMAT=text make run` (con `PDF_OXIDE_LIB_PATH`
exportado, ver [Binario local (sin Docker)](#binario-local-sin-docker)).

## Organización del código

```text
cmd/server                  composición de configuración, HTTP y apagado
internal/domain             entidades y errores del dominio
internal/usecase            reglas de negocio y cola de trabajadores
internal/infrastructure/pdf adaptador de pdf_oxide
internal/presentation/http  controladores, validación y control de admisión
internal/config             configuración de entorno y de Consul (fail-fast)
internal/logging            logger estructurado, request ID y saneado
internal/consul             cliente HTTP del KV y registro de servicio
internal/reqctx             contexto de petición (request ID)
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

Los scripts usan los cuatro PDF de `tests/stress/pdfs/` y apuntan por defecto a
`http://127.0.0.1:8080`. Ambos aceptan `TARGET` y `CORPUS`; el script de Vegeta
acepta además `RATE`, `DURATION`, `TIMEOUT` y `WORKERS`, mientras que el de k6
usa el perfil fijo de `spike.js` (rampa a 100 VUs). Para TLS, `vegeta` usa
`INSECURE=false` y k6 `INSECURE_TLS=false`; con el Traefik compartido el destino
es `https://extract.universidad.localhost` y el certificado de mkcert puede
requerir configurar `SSL_CERT_FILE` o dejar el modo inseguro de la herramienta.

## Resultados medidos

Con cinco réplicas (`cpus: 1.0`), `MAX_WORKERS=2` y `QUEUE_SIZE=56`, el perfil
de k6 (100 VUs) midió ~15 req/s con **0,00 % de errores** y la carga fija de
Vegeta (50 req/s) sirvió el **100 % de las peticiones** con p50 ~2,4 s. Esas
cotas no son los defaults del código: se configuran en `.env` (o con `-e` al
correr un contenedor) para reproducir la medición. La extracción con `pdf_oxide` cuesta
~4× más CPU por petición que la versión anterior con PDFium (~230 ms contra
~52 ms), así que con la cuota total de 5 CPUs el techo de throughput queda en
~15–20 req/s: CPU-bound, no de cola ni de transporte. El informe técnico
contiene las tablas completas, las condiciones de medición y el análisis del
cuello de botella.
