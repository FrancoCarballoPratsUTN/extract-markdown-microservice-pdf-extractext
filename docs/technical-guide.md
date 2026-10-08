# Guía técnica: microservicio de PDF a Markdown

## Arquitectura

El servicio utiliza una arquitectura de tres capas:

- **Presentación HTTP** (`internal/presentation/http`): recibe solicitudes,
  valida el cuerpo, aplica el control de admisión y devuelve respuestas RFC 9457.
- **Casos de uso** (`internal/usecase`): coordina la extracción y administra la
  cola acotada de trabajos.
- **Infraestructura** (`internal/infrastructure/pdf`): integra `pdf_oxide`
  (binding oficial de Go) y realiza la extracción en memoria, en el mismo
  proceso que el servidor.

Las dependencias apuntan hacia el dominio. La capa de casos de uso declara la
interfaz `Extractor`; la infraestructura la implementa. Así, el código de
negocio puede probarse sin depender de HTTP ni del motor de PDF.

## Decisiones principales

- **pdf_oxide en proceso (purego):** el motor Rust se carga en tiempo de
  ejecución vía `dlopen` (`PDF_OXIDE_LIB_PATH`), por lo que el binario se
  compila con `CGO_ENABLED=0` y no hay proceso worker ni RPC: una petición
  hace `OpenFromBytes` → `PageCount` → `ToMarkdownAll` y devuelve Markdown.
- **Aislamiento en proceso:** el adaptador mapea las sentinels del binding a
  errores de dominio (PDF corrupto → 400, tope de páginas → 413) y ejecuta la
  conversión bajo `recover()` para que un fallo nativo no mate la réplica; la
  biblioteca `libpdf_oxide` (Rust) no expone el modelo de memoria frágil que
  motivó el aislamiento por proceso de la versión anterior.
- **Cola acotada:** separa las solicitudes HTTP del trabajo intensivo de CPU y
  limita tanto la cantidad de trabajos como los bytes retenidos en memoria.
- **Control de admisión:** el semáforo HTTP rechaza temprano cuando la réplica
  está llena y devuelve `503 Service Unavailable`.
- **RFC 9457:** los errores de la API usan `application/problem+json`.
- **Aplicación sin estado:** la configuración proviene de variables de entorno,
  los registros se escriben en la salida estándar y no se guardan archivos
  temporales.

## Configuración y despliegue

La imagen Docker usa varias etapas: instala `libpdf_oxide` con el installer
pineado, compila el servidor (`CGO_ENABLED=0`), ejecuta las pruebas con la
librería real y construye una imagen final mínima sobre `bookworm-slim` con un
usuario sin privilegios.

```bash
docker compose up --build
```

La composición local inicia cinco réplicas detrás de Traefik y publica
`127.0.0.1:8080`. Para usar una instancia de Traefik existente:

```bash
docker compose -f docker-compose.yml -f docker-compose.traefik.yml up --build
```

Las variables principales (`MAX_PAGES`, `MAX_UPLOAD_MB`, `MAX_WORKERS`,
`QUEUE_SIZE`, `QUEUE_MAX_MB`, `PDF_OXIDE_LIB_PATH`) están documentadas en el
`README`.

## Pruebas

La etapa de pruebas de la imagen ejecuta análisis estático, pruebas unitarias y
pruebas de integración contra la librería real de pdf_oxide:

```bash
docker build --target test .
```

Localmente, `make test` corre `go vet` y las unitarias con `CGO_ENABLED=0`; las
de integración usan el tag `integration` y requieren `PDF_OXIDE_LIB_PATH`
apuntando a `libpdf_oxide.so` (`make test-integration`; ver `README` y
`AGENTS.md`).

## Pruebas de carga

Los scripts de `scripts/k6` y `scripts/vegeta` usan los cuatro PDF de
`tests/stress/pdfs/`. Ambos permiten sobrescribir el destino, el corpus, la tasa,
la duración y el tiempo de espera mediante variables de entorno.

```bash
k6 run scripts/k6/spike.js
./scripts/vegeta/attack.sh
```

Las mediciones, el análisis del cuello de botella y la justificación de los
valores `MAX_WORKERS=2` y `QUEUE_SIZE=56` se encuentran en
**[INFORME.md](../INFORME.md)**.
