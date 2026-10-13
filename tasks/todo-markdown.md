# TODO — Estrategia A: binding Go in-process (pdf_oxide)

Leyenda: `[ ]` pendiente · `[~]` en curso · `[x]` hecho
Dependencias: T1 → T2 → T3 → T4 → T5 → T6 → T7 → T8 → T9

---

## Fase 0 — Spike / gate

### [x] T1 — Binding Go + probe de corpus
- **Qué**: `go list -m -versions github.com/yfedoseev/pdf_oxide/go`;
  `go get` con versión pineada; installer (`-shared` para purego o CGO flags
  para cgo); probe en módulo aislado `/tmp/opencode/goprobe` que ejecute
  `OpenFromBytes` → `PageCount == 16` → `ToMarkdownAll()` con títulos `# `
  sobre el corpus, y verifique sentinela de error en PDF corrupto.
- **Decisión**: elegir backend purego vs cgo (criterios: env mínimo en
  `go vet`/`go test`, simplicidad Docker) y documentarlo aquí.
- **AC**: probe verde; env dev documentado; resultado del backend anotado.
- **Verificación**: `go run` del probe imprime page_count=16, headings, ms.
- **Gate**: si falla → abortar; fallback estrategia B (worker Rust, spike
  previo validado).
- **Archivos**: ninguno en el repo (scratch en `/tmp/opencode`).
- **Resultado (2026-10-07)**: ✅ verde. Versión pineada **v0.3.78** (misma
  del crate). **Backend: purego** (`CGO_ENABLED=0` +
  `PDF_OXIDE_LIB_PATH=$HOME/.cache/pdf_oxide/v0.3.78/lib/linux_amd64/libpdf_oxide.so`).
  Datos: 16 pág, 37036 bytes (idéntico al core Rust), 4× `# ` + títulos `##`,
  sin `\r`; corrupto → `errors.Is(ErrInvalidFormat)` ✓; **~136 ms/request**
  (open+pagecount+ToMarkdownAll) y ~110 ms por conversión cálida →
  capacidad ~73 RPS con MAX_WORKERS=10 > 25,35 req/s del baseline.

---

## Fase 1 — Adaptador (TDD)

### [x] T2 — Tests RED del extractor
- **Qué**: reescribir `extractor_test.go` (unit: mapeo sentinels →
  `ErrInvalidPDF`/`ErrExtractionFailed`, sanitize CRLF/controles/trim,
  recover ante panic, tope de páginas → `ErrTooLarge`) y
  `extractor_integration_test.go` (corpus: 16 páginas, `# `, sin `\r`, sin
  controles de formato, sin whitespace final).
- **AC**: tests fallan contra el extractor actual (texto crudo).
- **Verificación**: `go test ./internal/infrastructure/pdf/` → RED.
- **Archivos**: `internal/infrastructure/pdf/extractor_test.go`,
  `extractor_integration_test.go`.

### [x] T3 — Extractor con pdf_oxide (GREEN)
- **Qué**: implementar `extractor.go`: `OpenFromBytes` → `PageCount` +
  tope `maxPages` → `ToMarkdownAll` → `sanitize`; mapeo de sentinels;
  `defer recover` → `ErrExtractionFailed`.
- **AC**: T2 en verde; firma del puerto `usecase.Extractor` intacta.
- **Verificación**: `go test ./internal/infrastructure/pdf/` y
  `go test -tags integration ...` → GREEN.
- **Archivos**: `internal/infrastructure/pdf/extractor.go`.

### [x] T4 — Wiring, config y handler
- **Qué**: `buildExtractor` sin pool (devuelve extractor + probe de arranque
  que fatal si la lib no carga); config: borrar `PdfiumWorkerPath`,
  `pdfiumWorkerName`, `defaultWorkerPath`, `currentExecutable`; agregar
  `MAX_PAGES` (default 2000, 0 = sin límite); handler: `ErrTooLarge` → 413.
- **AC**: sin referencias a PDFIUM_WORKER_PATH; `MAX_PAGES` efectivo.
- **Verificación**: `go vet ./... && go test ./...` (tests de config y
  handler actualizados).
- **Archivos**: `cmd/server/main.go`, `internal/config/config.go` (+tests),
  `internal/presentation/http/handler.go` (+`handler_test.go`).

---

## Fase 2 — Limpieza y build

### [x] T5 — Borrar legacy de PDFium
- **Qué**: eliminar `internal/infrastructure/pdf/pool.go`, `pool_test.go`,
  `cmd/pdfium-worker/`; `go mod tidy` (cae go-pdfium y su árbol indirecto).
- **AC**: `go build ./...` sin go-pdfium en go.mod; suites verdes.
- **Verificación**: `go vet ./... && go test ./...`.
- **Archivos**: (borrado) + `go.mod`/`go.sum`.

### [x] T6 — Dockerfile + comandos dev
- **Qué**: quitar stage `pdfium`/ARG PDFIUM_VERSION; installer pineado de
  `libpdf_oxide` (ruta fija); builder según backend elegido; runtime =
  debian-slim + server (+ `.so` si purego, con `PDF_OXIDE_LIB_PATH`);
  test stage incluye `-tags integration`. Makefile/AGENTS.md con `make test`,
  `make test-integration`.
- **AC**: `docker build --target test .` verde; imagen runtime levanta.
- **Verificación**: docker build de test + run de runtime con smoke.
- **Archivos**: `Dockerfile`, `Makefile`/`AGENTS.md`.

---

## Fase 3 — Verificación

### [x] T7 — Suites completas + smoke
- **AC**: vet + tests unitarios + integration-tags + docker test verdes;
  smoke manual: POST corpus → 200 con JSON `content` en Markdown +
  `page_count`; payload corrupto → 400.
- **Verificación**: comandos de §5 del plan + curl manual.

### [x] T8 — Métricas vs baseline
- **AC**: k6 ≥ 25,35 req/s, 0,00% errores, p50 < 1,88s, p90 < 7,83s,
  p95 < 8,80s, max < 13,94s; vegeta success > 66,53%.
- **Verificación**: k6 + vegeta; resultados guardados para el INFORME.
- **Resultado (2026-10-08)**: corridas con compose intacto (5 réplicas ×
  `cpus: 1.0`), perfil del baseline. k6: 14,88–15,05 req/s, **0,00% errores ✓**,
  max 13,16–13,26s **< 13,94 ✓**; p50 5,06–5,25s, p90 ~10,2s, p95 ~12,1s
  **✗** y throughput **✗**. Vegeta: **100% éxito ✓** (>66,53%), p50 2,36s,
  p90 7,31s, máx 9,20s. Diagnóstico: CPU-bound — `ToMarkdownAll` cuesta
  ~226 ms/req (4× los 51,5 ms de pdfium), techo teórico 5 CPUs ÷ 0,226 s
  ≈ 22 req/s < 25,35. Documentado en INFORME §5.7; superarlo requiere
  ampliar la cuota de CPU (fuera de alcance: compose intacto).

### [x] T9 — Documentación
- **Qué**: INFORME.md (§4 arquitectura sin PDFium/pool + §5 métricas
  nuevas), README (setup/env: quitar PDFIUM_WORKER_PATH, documentar
  instalación de libpdf_oxide), checkboxes de este archivo.
- **AC**: docs consistentes con el código; INFORME con números de T8.
- **Resultado (2026-10-08)**: INFORME §1/§2/§3 (nota histórica)/§5.7
   reescritos para `pdf_oxide` in-process; README y docs/technical-guide.md
   sin rastros de PDFIUM_WORKER_PATH; Makefile con `install`/`test` según
   AGENTS.md.

---

# Parte 2 — Observabilidad y operación (logging + apagado seguro + entorno)

Leyenda: `[ ]` pendiente · `[~]` en curso · `[x]` hecho
Dependencias: T10,T11,T12 → T13 → T14 → T15 → T16 → T17 → T18 → T19 → T20 → T21 → T22 → T23

Plan: `tasks/plan-markdown.md` (Parte 2). No editar `tasks/plan.md` ni
`tasks/todo.md`.

---

## Fase 0 — Fundaciones

### [x] T10 — Paquete `internal/logging`
**Description:** Crear el paquete `logging` replicando verbatim el del
orquestador: `Options{Level, Format, Output}`, `New()` (lee `LOG_LEVEL`/
`LOG_FORMAT`, un valor inválido devuelve error), `NewWithOptions`,
`Discard`, `ParseLevel`, `ValidateFormat`. Defaults `info`/`json`.

**Acceptance criteria:**
- [ ] `New()` con `LOG_LEVEL`/`LOG_FORMAT` inválidos devuelve error.
- [ ] `NewWithOptions` emite JSON por defecto y respeta `Format="text"`.
- [ ] `Discard()` devuelve un logger que no escribe nada.

**Verification:**
- [ ] `make test-unit` verde (nuevo `internal/logging/logging_test.go`).
- [ ] `make build` verde.

**Dependencies:** None
**Files likely touched:** `internal/logging/logging.go`, `internal/logging/logging_test.go`
**Estimated scope:** S (2 files)

### [x] T11 — Paquete `internal/reqctx`
**Description:** Crear `reqctx` replicando el del orquestador: `WithRequestID`,
`RequestID(ctx)`, `Logger(ctx, logger)` (adjunta `request_id` si está presente;
si no, devuelve el logger original).

**Acceptance criteria:**
- [ ] `RequestID` devuelve cadena vacía sin valor o con tipo inválido.
- [ ] `Logger` añade `request_id` solo cuando existe.

**Verification:**
- [ ] `make test-unit` verde (nuevo `internal/reqctx/reqctx_test.go`).

**Dependencies:** None
**Files likely touched:** `internal/reqctx/reqctx.go`, `internal/reqctx/reqctx_test.go`
**Estimated scope:** S (2 files)

### [x] T12 — Config centralizada fail-fast + `.env`
**Description:** Refactorizar `internal/config` al patrón del orquestador:
`Load() (Config, error)` + `fromEnv(getenv func(string) string)` inyectable;
validación estricta (`validatePort`, `parseDuration`, `parseIntNonNegative`) que
aborta el arranque ante valores inválidos (hoy `getEnvInt` los ignora). Añadir
`ShutdownTimeout` (default `35s`). Crear `.env`/`.env.example` con el inventario
completo; actualizar `docker-compose.yml` a `${VAR:-default}` (+`LOG_LEVEL`,
`LOG_FORMAT`, `SHUTDOWN_TIMEOUT`, `STOP_GRACE_PERIOD`) y `.dockerignore` (`.env`).

**Acceptance criteria:**
- [ ] `Load()` conserva los nombres actuales (`PORT`, `MAX_UPLOAD_MB`, `MAX_PAGES`,
  `MAX_WORKERS`, `QUEUE_SIZE`, `QUEUE_MAX_MB`, `READ_TIMEOUT`, `WRITE_TIMEOUT`) y
  valida cada uno; valor inválido ⇒ error.
- [ ] `MAX_PAGES=0` sigue deshabilitando el límite.
- [ ] `.env.example` documenta todas las variables; `.env` está git-ignored e
  ignorado por Docker.
- [ ] Compose usa `${VAR:-default}` (entorno real gana sobre `.env`).

**Verification:**
- [ ] `make test-unit` verde (`internal/config/config_test.go` con `fromEnv` inyectado: defaults + cada error).
- [ ] `docker compose config` válido; `PORT=9090` en el shell gana sobre `.env`.

**Dependencies:** None
**Files likely touched:** `internal/config/config.go`, `internal/config/config_test.go`, `.env`, `.env.example`, `docker-compose.yml`, `.dockerignore`
**Estimated scope:** M (6 files)

---

## Checkpoint A (tras T10–T12)
- [x] `make test-unit` y `make build` verdes.
- [x] Valor de entorno inválido aborta el arranque con mensaje claro.
- [x] `.env` local se usa en Compose; el entorno real tiene precedencia.

---

## Fase 1 — Observabilidad HTTP

### [x] T13 — Helper `logDecision`
**Description:** Crear `internal/presentation/http/logging.go` con
`logDecision(ctx, logger, msg, status, attrs...)`: deriva el nivel del status
(5xx→Error, 4xx→Warn, resto→Info), antepone `status` y mergea `request_id` vía
`reqctx.Logger`. Fallback a `slog.Default()` si `logger == nil`.

**Acceptance criteria:**
- [ ] 200/201 ⇒ INFO; 400/404/413 ⇒ WARN; 500 ⇒ ERROR.
- [ ] La línea incluye `status` y `request_id` cuando hay contexto.

**Verification:**
- [ ] `make test-unit` verde (nuevo `logging_test.go` con buffer).

**Dependencies:** T10, T11
**Files likely touched:** `internal/presentation/http/logging.go`, `internal/presentation/http/logging_test.go`
**Estimated scope:** S (2 files)

### [x] T14 — `withRequestID` + `withRecovery`
**Description:** En `middleware.go`, añadir `withRequestID` (eco de
`X-Request-ID` o `newRequestID()` con `crypto/rand`, dejado en `reqctx` y en la
respuesta) y `withRecovery(logger)` (`ERROR "panic recovered"` con `panic`,
`method`, `path`, `stack`, y problem 500).

**Acceptance criteria:**
- [ ] El header `X-Request-ID` se ecoa o genera y aparece en el contexto.
- [ ] Un panic en el handler produce 500 y una línea de error con `stack`.

**Verification:**
- [ ] `make test-unit` verde (`middleware_test.go`).

**Dependencies:** T11
**Files likely touched:** `internal/presentation/http/middleware.go`, `internal/presentation/http/middleware_test.go`
**Estimated scope:** S (2 files)

### [x] T15 — `withRequestLog` + `statusRecorder`
**Description:** Access log en `middleware.go`: `withRequestLog(logger)`,
`statusRecorder` (captura `status`/`bytes`, con `Flush`) y `accessLogLevel`.
Línea `"http request"` con `method`, `path`, `status`, `bytes`,
`remote_addr`, `request_id`, `duration_ms`.

**Acceptance criteria:**
- [ ] Una request produce exactamente una línea `"http request"` con `status` y `duration_ms`.
- [ ] El nivel sigue el status (5xx→Error, 4xx→Warn, resto→Info).

**Verification:**
- [ ] `make test-unit` verde (`middleware_test.go` con buffer).

**Dependencies:** T11, T14
**Files likely touched:** `internal/presentation/http/middleware.go`, `internal/presentation/http/middleware_test.go`
**Estimated scope:** S (2 files)

### [x] T16 — Logging en `handler.go`
**Description:** Inyectar `logger` en `NewHandler`; en cada rama de rechazo
llamar `logDecision` con el status y mensajes tipo
`"pdf rejected: body exceeds size limit"`, `"pdf rejected: invalid request"`,
`"pdf rejected: malformed pdf"`, `"pdf rejected: exceeds page limit"`,
`"extraction unavailable"`, `"extraction failed"`. No se loguea el contenido del PDF.

**Acceptance criteria:**
- [ ] Cada respuesta de error (400/405/413/500/503) deja una línea con su status.
- [ ] No se registran bytes del cuerpo ni rutas de fichero.

**Verification:**
- [ ] `make test-unit` y `make test` verdes (`handler_test.go`).

**Dependencies:** T13
**Files likely touched:** `internal/presentation/http/handler.go`, `internal/presentation/http/handler_test.go`
**Estimated scope:** S (2 files)

---

## Checkpoint B (tras T13–T16)
- [x] Request exitosa ⇒ una línea `"http request"` con `request_id`.
- [x] 4xx ⇒ WARN; 5xx ⇒ ERROR, con motivo y `request_id`.

---

## Fase 2 — Logging del pipeline

### [x] T17 — `WorkerPool`: logger + `Submit(ctx, data)`
**Description:** Inyectar `logger`; loguear `INFO "worker pool started"`
(`workers`, `queue_size`, `queue_max_bytes`) al construir. Cambiar
`Submit(data)` → `Submit(ctx, data)` guardando `reqctx.RequestID(ctx)` en el
`job`. En budget/cola agotados: `WARN "queue full; rejecting job"`. En `work()`:
`DEBUG "extraction completed"` (`pages`, `duration_ms`) o
`ERROR "extraction failed"` con `request_id`. Actualizar todos los tests del
pool y adaptadores.

**Acceptance criteria:**
- [ ] Cola llena ⇒ `WARN` correlacionado y `ErrQueueFull`.
- [ ] Éxito ⇒ `DEBUG` con `pages`/`duration_ms` y `request_id`.
- [ ] Emisión de bytes inválida ⇒ `WARN "queue full; rejecting job"`.

**Verification:**
- [ ] `make test` verde (`workerpool_test.go`, `workerpool_queue_test.go`,
  `workerpool_concurrency_test.go`, `queued_extractor_test.go`).

**Dependencies:** T10, T11
**Files likely touched:** `internal/usecase/workerpool.go`, `internal/usecase/workerpool_test.go`, `internal/usecase/workerpool_queue_test.go`, `internal/usecase/workerpool_concurrency_test.go`, `internal/usecase/queued_extractor_test.go`
**Estimated scope:** M (5 files)

### [x] T18 — `queued_extractor`: cancel/deadline
**Description:** Inyectar `logger` en `NewQueuedExtractor`; usar
`pool.Submit(ctx, data)`; loguear `WARN "extraction wait cancelled"` (`error`)
cuando `ctx` cancela o expira el deadline, antes de mapear a
`domain.ErrServiceUnavailable`.

**Acceptance criteria:**
- [ ] Deadline/cancelación dejan una línea `WARN` con `error`.

**Verification:**
- [ ] `make test` verde (`queued_extractor_test.go`).

**Dependencies:** T17
**Files likely touched:** `internal/usecase/queued_extractor.go`, `internal/usecase/queued_extractor_test.go`
**Estimated scope:** S (2 files)

### [x] T19 — `extractor`: logger + panic
**Description:** Inyectar `logger` en `NewExtractor`; en el `recover()` emitir
`ERROR "panic recovered while extracting"` con `stack`. Actualizar tests y la
llamada de integración.

**Acceptance criteria:**
- [ ] Un panic capturado deja una línea `ERROR` con `stack` y retorna `ErrExtractionFailed`.

**Verification:**
- [ ] `make test` verde (`extractor_test.go`, `extractor_integration_test.go`).
- [ ] `make test-integration` verde con `PDF_OXIDE_LIB_PATH`.

**Dependencies:** T10
**Files likely touched:** `internal/infrastructure/pdf/extractor.go`, `internal/infrastructure/pdf/extractor_test.go`, `internal/infrastructure/pdf/extractor_integration_test.go`
**Estimated scope:** S (3 files)

---

## Checkpoint C (tras T17–T19)
- [x] Cola llena ⇒ `WARN` correlacionado.
- [x] Extracción OK/fallida loguea `request_id`, `pages`, `duration_ms`.
- [x] Panic ⇒ `ERROR` con `stack`, respuesta 500.

---

## Fase 3 — Apagado seguro + wiring de `main`

### [x] T20 — `main`: logger, `run`/`runWithSignals`, `logStartup`
**Description:** `main` construye el logger con `logging.New()` (a stderr +
`os.Exit(1)` si inválido). Refactor a `run(logger) error` y
`runWithSignals(ctx, logger, cfg, stopCh)` inyectando el logger en
`NewExtractor`, `NewWorkerPool`, `NewHandler`, `NewAdmissionMiddleware`.
`logStartup(logger, cfg)` con `"application starting"` y la config efectiva
(port, límites, timeouts; sin secretos). Mensajes `"server listening"` y
`"application stopped"` (error) con código ≠ 0.

**Acceptance criteria:**
- [ ] Arranque deja una línea `"application starting"` con la config efectiva.
- [ ] El logger llega a handler, pool, extractor y admisión.
- [ ] `logging.New()` inválido ⇒ stderr y `os.Exit(1)`.

**Verification:**
- [ ] `make build` verde; `LOG_LEVEL=debug ./bin/server` emitido en JSON.
- [ ] `cmd/server/main_test.go` (nuevo, si aplica) verde.

**Dependencies:** T10, T12, T14, T15, T16, T17, T18, T19
**Files likely touched:** `cmd/server/main.go`, `cmd/server/main_test.go` (nuevo)
**Estimated scope:** M (2-3 files)

### [x] T21 — Apagado acotado + `stop_grace_period`
**Description:** Añadir `WorkerPool.CloseContext(ctx)` (o equivalente) que drene
con deadline en vez del `defer jobs.Close()` sin límite. Usar `SHUTDOWN_TIMEOUT`
en `srv.Shutdown`. Un segundo `SIGTERM`/`SIGINT` fuerza `os.Exit(1)`. Loguear
`"shutting down"` (`signal`), `"http shutdown did not complete"` (`error`),
`"worker pool drained"`. Fijar `stop_grace_period` > `SHUTDOWN_TIMEOUT` en compose.

**Acceptance criteria:**
- [ ] `SIGTERM` ⇒ drenado en ≤ `SHUTDOWN_TIMEOUT`; salida 0 si todo drena.
- [ ] Timeout agotado ⇒ salida ≠ 0 + log de error.
- [ ] Segundo `SIGTERM` sale de inmediato.
- [ ] `stop_grace_period` > `SHUTDOWN_TIMEOUT` en compose.

**Verification:**
- [ ] `make test` verde (`cmd/server/main_test.go` con `runWithSignals` + `stopCh`).
- [ ] Prueba manual: `kill -TERM` y `kill -TERM` doble.

**Dependencies:** T20, T17
**Files likely touched:** `cmd/server/main.go`, `internal/usecase/workerpool.go`, `docker-compose.yml`, `cmd/server/main_test.go`
**Estimated scope:** M (4 files)

---

## Checkpoint D (tras T20–T21)
- [x] `SIGTERM` ⇒ `"shutting down"` + `"worker pool drained"` + salida 0.
- [x] `SHUTDOWN_TIMEOUT` agotado ⇒ salida ≠ 0 y log de error.
- [x] Segundo `SIGTERM` fuerza salida.

---

## Fase 4 — Docs y verificación

### [x] T22 — Documentación
**Description:** README: sección **Logging** (formato, `LOG_LEVEL`/`LOG_FORMAT`,
`request_id`, access log) y tabla de variables como el orquestador.
`AGENTS.md`: sección Environment con `LOG_LEVEL`, `LOG_FORMAT`,
`SHUTDOWN_TIMEOUT`. Corregir la afirmación falsa en `tasks/todo.md:157`.

**Acceptance criteria:**
- [ ] README y `AGENTS.md` listan todas las variables y su default.
- [ ] `tasks/todo.md:157` deja de afirmar logs inexistentes (con nota al plan nuevo).

**Verification:**
- [ ] Revisión manual de README/AGENTS.

**Dependencies:** T10, T12, T15, T20, T21
**Files likely touched:** `README.md`, `AGENTS.md`, `tasks/todo.md`
**Estimated scope:** S (3 files)

### [x] T23 — Verificación integral
**Description:** Corrida completa de gates y pruebas de humo.

**Acceptance criteria:**
- [x] `make test`, `make test-unit`, `make build` verdes.
- [x] `docker build --target test .` verde.
- [x] Smoke `LOG_LEVEL=debug`: PDF válido/200, no-PDF/400, > `MAX_UPLOAD_MB`/413,
  con `request_id` correlacionable por `X-Request-ID`.
- [x] Precedencia: `PORT=9090` en el entorno gana sobre `.env`.

**Verification:**
- [x] Registrar resultados (comandos + salidas clave) en este task o en el plan.

**Resultados (2026-10-12):**
- `make test`, `make test-unit`, `make build`: verdes (vet + tests + unit con `-tags unit`).
- `docker build --target test .`: verde; la etapa `test` corrió `go vet`, `go test`,
  `go test -tags unit` y `go test -tags integration` (con `libpdf_oxide.so` real).
- Smoke con `PORT=18081 MAX_UPLOAD_MB=1 LOG_LEVEL=debug`:
  - PDF válido + `X-Request-ID: smoke-abc` ⇒ `200`, `page_count=16`; la misma
    `request_id` aparece en `"extraction completed"` (DEBUG, `pages=16`,
    `duration_ms`) y en el access log `"http request"` (INFO, `status=200`).
  - `Content-Type: text/html` ⇒ `400` (`unsupported media type`).
  - 1,5 MB con `application/pdf` ⇒ `413` (`pdf file exceeds maximum size`).
  - `kill -TERM` ⇒ `"shutting down"` + `"worker pool drained"` y salida `0`.
- Precedencia: `PORT=9090 docker compose config` resuelve `PORT: "9090"`; sin
  override resuelve `PORT: "8080"` (desde `.env`). `stop_grace_period: 45s`.

**Dependencies:** T10–T22
**Files likely touched:** ninguno (solo verificación)
**Estimated scope:** S

---

## Checkpoint Final
- [x] Todos los AC cumplidos.
- [x] `make test` + `make test-unit` + `make build` + `docker build --target test .` verdes.
- [ ] Revisión con humano antes de cerrar.
