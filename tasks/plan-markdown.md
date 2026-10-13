# Plan: extracción de PDF a Markdown — Estrategia A (binding Go in-process)

> Reemplaza al plan de la estrategia B (worker Rust). El core `pdf_oxide` ya
> fue validado en spike (corpus Scrum Guide: 16 páginas, 28 títulos detectados).
> Existen bindings oficiales de Go: `github.com/yfedoseev/pdf_oxide/go`
> (`pdfoxide`), con el mismo core Rust.
>
> **Paso 0 ejecutado**: se revirtió el crate `pdf-worker/` y la entrada de
> `.gitignore` asociada. `tasks/plan.md` y `tasks/todo.md` originales intactos.

## 1. Objetivo

Reemplazar la extracción en texto crudo de PDFium (go-pdfium) por Markdown
estructurado (títulos, párrafos, listas — lo que pdf_oxide detecta por defecto)
usando el binding oficial de Go, **sin pool de workers ni proceso separado**,
conservando los mecanismos de protección en proceso y superando las métricas
baseline del INFORME.

## 2. Baseline a superar (INFORME §5.4)

| Métrica | Baseline |
|---|---|
| k6 throughput | ≥ 25,35 req/s |
| k6 errores | 0,00% (hoy: 1,62% de 503 — punto débil) |
| k6 p50 / p90 / p95 / max | < 1,88s / 7,83s / 8,80s / 13,94s |
| Vegeta success | > 66,53% (hoy 98,4–99,3%) |
| Extracción unitaria | hoy 51,5 ms (67 llamadas FFI) → esperado ~ms con pdf_oxide (0,8 ms mean declarado) |

## 3. Decisiones de diseño

1. **Estrategia A**: `pdfoxide.OpenFromBytes` → `PageCount` → `ToMarkdownAll`
   dentro del proceso del server. Se elimina go-pdfium, `cmd/pdfium-worker`,
   `internal/infrastructure/pdf/pool.go` y la etapa de PDFium del Dockerfile.
2. **Aislamiento en proceso** (conservado + nuevo):
   - **Conservado** (ya existe, independiente de PDFium): `WorkerPool`
     (semáforo de `MAX_WORKERS` extracciones concurrentes + byte budget
     `QUEUE_MAX_MB` anti-OOM), `AdmissionMiddleware`, deadlines,
     `MAX_UPLOAD_MB`.
   - **Nuevo**: `recover()` en el adaptador; tope de páginas `MAX_PAGES`
     (default 2000, 0 = sin límite); mapeo `domain.ErrTooLarge` → **413**
     (hoy está mapeado a 500, fix); probe de arranque en `buildExtractor`
     que verifica que la lib nativa cargue (fatal si no).
   - **Backstop duro**: límite de memoria del contenedor (docs).
   - **Riesgo residual aceptado**: una llamada nativa trabada no se puede
     cancelar con ctx; ocupa 1 worker hasta retornar (pdf_oxide declara
     "zero timeouts"; los deadlines ya hacen shed de clientes en espera).
3. **Backend (purego vs cgo)**: decisión empírica en T1 — criterios:
   `go vet`/`go test ./...` con env mínimo, simplicidad de Dockerfile.
   Preferencia inicial: purego (`CGO_ENABLED=0` + `PDF_OXIDE_LIB_PATH`).
4. **Markdown mínimo**: solo títulos/párrafos/listas (defaults de pdf_oxide).
   Tablas, enlaces y negritas fuera de contrato.
5. **Contrato de errores del adaptador**:

   | Condición | Sentinela pdfoxide | Dominio | HTTP |
   |---|---|---|---|
   | payload no parseable / no encontrado | `ErrInvalidFormat`, `ErrParseError`, `ErrInvalidPath`, `ErrDocumentNotFound` | `ErrInvalidPDF` | 400 |
   | páginas > `MAX_PAGES` | — | `ErrTooLarge` | 413 |
   | fallo de conversión / otro | `ErrExtractionFailed`, `ErrInternal`, otros | `ErrExtractionFailed` | 500 |
   | panic capturado | — | `ErrExtractionFailed` | 500 |

## 4. Fases y tareas (TDD: RED → GREEN → REFACTOR)

### Fase 0 — Spike / gate
- **T1**: `go list -m -versions` + `go get` pineado + installer. Probe en
  módulo aislado (`/tmp/opencode/goprobe`): `OpenFromBytes(corpus)` →
  `PageCount == 16` → `ToMarkdownAll()` con títulos `# ` + sentinela en PDF
  corrupto. Elegir backend (purego/cgo) y documentar env dev.
  **Gate: si el binding no funciona → abortar; fallback = estrategia B
  (worker Rust, ya validada en spike previo).**

### Fase 1 — Adaptador (TDD)
- **T2**: reescribir tests del extractor → RED: unit (mapeo de sentinels,
  sanitize CRLF/control-chars/trim, recover, tope de páginas) +
  integration-tag (corpus: 16 pág, `# `, sin `\r`, sin controles).
- **T3**: implementar `internal/infrastructure/pdf/extractor.go` → GREEN.
- **T4**: `buildExtractor` sin pool + probe de arranque; config: borrar
  `PdfiumWorkerPath`/`pdfiumWorkerName`, agregar `MAX_PAGES`; handler:
  `ErrTooLarge` → 413 (+ tests).

### Fase 2 — Limpieza y build
- **T5**: borrar `pool.go` + tests, `cmd/pdfium-worker/`, `go mod tidy`
  (cae go-pdfium, go-plugin, grpc…).
- **T6**: Dockerfile sin PDFium (installer pineado de `libpdf_oxide`, build
  según backend elegido); Makefile/AGENTS con comandos dev
  (`make test`, `make test-integration`).

### Fase 3 — Verificación
- **T7**: `go vet ./...` + `go test ./...` + `go test -tags integration` +
  `docker build --target test .` + smoke manual (200 markdown / corrupto 400).
- **T8**: k6 + vegeta vs baseline §5.4 → registrar números en el informe.
- **T9**: actualizar INFORME.md (arquitectura + métricas), README
  (setup/env: `PDFIUM_WORKER_PATH` fuera), checkboxes en
  `tasks/todo-markdown.md`.

## 5. Comandos de verificación

| Nivel | Comando |
|---|---|
| Unit + integración rápida | `go vet ./... && go test ./...` |
| Integración con lib nativa | `go test -tags integration -v -timeout 5m ./internal/infrastructure/pdf/` (requiere `PDF_OXIDE_LIB_PATH`) |
| Docker | `docker build --target test .` |
| Métricas | k6 + vegeta (scripts según INFORME §5) |

Los comandos finales con env se fijan en T6 (Makefile/AGENTS.md).

## 6. Riesgos

- Dependencia joven (bindings desde jun 2026) → versión pineada + fallback B.
- Versión del módulo Go puede ir por detrás del crate → verificar en T1.
- Red necesaria en build Docker (mismo patrón que pdfium hoy).
- Llamada nativa no cancelable (documentado en §3.2).

---

# Parte 2 — Observabilidad y operación (logging + apagado seguro + entorno)

> **Alcance.** Tres frentes que se apoyan en el mismo cableado:
> 1. **Logging estratégico** con `log/slog` (JSON a stdout), `request_id` por
>    petición y access log, replicando el estilo del **orquestador**
>    (`validator/ValidationMicroservices-pdf-extractext`).
> 2. **Apagado seguro**: timeout configurable, drenado acotado del WorkerPool,
>    segundo `SIGTERM` que fuerza salida y `stop_grace_period` coherente.
> 3. **Manejo centralizado de entorno**: `.env`/`.env.example`, `internal/config`
>    con validación *fail-fast* e inyección en tests, y precedencia
>    *entorno real > `.env`* para que en producción los valores lleguen "desde
>    arriba".
>
> No se edita `tasks/plan.md` ni `tasks/todo.md` (planes originales, intactos).

## 7. Objetivo (observabilidad)

Hoy el servicio solo emite 4 líneas `log.Printf` en texto plano en `main.go`.
Cualquier fallo interno (saturación, cola llena, PDF inválido, panic) es
invisible salvo por el access log del edge (Traefik), como documenta el INFORME
§5.5. Esta parte le da al servicio observabilidad propia, un apagado predecible
y una configuración de entorno única y validada.

## 8. Estado actual (puntos de dolor)

- **Sin access log ni correlación**: no hay `request_id`; el `503` de saturación
  (`middleware.go:35`) y el de cola llena (`workerpool.go:124`) se confunden.
- **`todo.md` miente**: `tasks/todo.md:157` marca `[x] "Logs to stdout (JSON or
  structured)"`, pero `plan.md:16` ("Logging: JSON to stdout") no está
  implementado.
- **Apagado no acotado**: `defer jobs.Close()` (`main.go:44`) hace `wg.Wait()`
  sin deadline (`workerpool.go:150-157`); `srv.Shutdown` usa 10s hardcodeados;
  sin `stop_grace_period`, Docker hace `SIGKILL` a los 10s (default).
- **Config silenciosa**: `config.getEnvInt` ignora valores inválidos y cae al
  default sin avisar; sin `.env` ni `.env.example`.

## 9. Decisiones de diseño (observabilidad)

1. **Replicar el orquestador**, no reinventar:
   - `internal/logging` (paquete `logging`): `New`, `NewWithOptions`, `Discard`,
     `ParseLevel`, `ValidateFormat`, `Options{Level, Format, Output}`.
     Env `LOG_LEVEL` (default `info`) y `LOG_FORMAT` (default `json`); un valor
     inválido **aborta el arranque**.
   - `internal/reqctx` (paquete `reqctx`): `WithRequestID`, `RequestID`,
     `Logger(ctx, logger)`.
   - La config del log vive en `internal/logging`, **no** en `internal/config`
     (decisión explícita del orquestador, `logging.go:24-26`).
2. **Middleware en `internal/presentation/http`** con los nombres del
   orquestador: `withRequestID`, `newRequestID`, `withRecovery`,
   `withRequestLog`, `statusRecorder`, `accessLogLevel`; helper `logDecision`
   en `internal/presentation/http/logging.go`. No se crea `internal/server`
   (el extract no tiene router/Grupo; ya se decidió mantenerlos en la capa HTTP).
3. **Orden del stack**:
   `withRequestID → withRecovery → withRequestLog → Admission → WithDeadline →
   handler`. El `request_id` existe antes que recovery y access log; el `503`
   de saturación queda correlacionado.
4. **Nivel por status**: 5xx ⇒ ERROR, 4xx ⇒ WARN, resto ⇒ INFO (`logDecision`
   y `accessLogLevel`), igual que el orquestador.
5. **`request_id` en el pool**: `WorkerPool.Submit(ctx, data)` arrastra
   `reqctx.RequestID(ctx)` en el `job`; el worker loguea con
   `logger.With("request_id", j.requestID)` (el worker corre con
   `context.Background()` por diseño, así que no puede leerlo del ctx).
6. **Constructores con `logger *slog.Logger`**; los tests pasan
   `logging.Discard()` o un logger a `bytes.Buffer`. Variante `NewWithLogger`
   solo donde haga falta conservar una firma `New` sin logger.
7. **Config fail-fast** (§ entorno): `Load() (Config, error)` +
   `fromEnv(getenv func(string) string)` inyectable. Valor inválido ⇒ error ⇒
   `main` aborta. Se mantienen los nombres `MAX_UPLOAD_MB`/`QUEUE_MAX_MB`
   (enteros MB) para no romper compose/INFORME, pero validados.
8. **Precedencia de entorno**: `.env` para local (lo consume Docker Compose vía
   `${VAR:-default}`), entorno real gana; en la imagen no viaja `.env`. Se
   añade `.env` a `.dockerignore`.
9. **Apagado**: `SHUTDOWN_TIMEOUT` configurable (default `35s`, > `WRITE_TIMEOUT`
   de 30s); `run(logger) error` + `runWithSignals(ctx, logger, cfg, stopCh)`
   (patrón orquestador, testeable); drenado del pool con deadline
   (`CloseContext`); segundo `SIGTERM`/`SIGINT` ⇒ `os.Exit(1)`.
10. **`CLOSE`/`OPEN` question**: se ejecuta el plan con las recomendaciones por
    defecto: godotenv **no** se agrega (Compose-only como el orquestador);
    mantener MB enteros validados; apagado completo. Ver §13.

## 10. Grafo de dependencias

```
logging ─┐
reqctx ──┼─→ middleware logging ─→ handler logging ─┐
config ──┘                                          ├─→ main wiring ─→ apagado seguro
                          pool/extractor logging ───┘                         │
                                                                              ▼
                                                                    docs + verificación
```

Orden de implementación (bottom-up): fundaciones → observabilidad HTTP →
logging del pipeline (pool/extractor) → apagado + wiring de `main` → docs y
verificación.

## 11. Task list (índice)

Detalle de cada tarea en `tasks/todo-markdown.md` (Parte 2).

### Fase 0 — Fundaciones
- T10: paquete `internal/logging` (+ tests).
- T11: paquete `internal/reqctx` (+ tests).
- T12: `internal/config` fail-fast + `.env`/`.env.example` + compose + dockerignore (+ tests).

### Checkpoint A (tras T10–T12)
- [x] `make test-unit` verde; `make build` verde.
- [x] `.env` local se lee vía Compose; entorno real gana sobre `.env`.
- [x] Valor inválido de env aborta el arranque.

### Fase 1 — Observabilidad HTTP
- T13: `logDecision` en `internal/presentation/http/logging.go` (+ tests).
- T14: `withRequestID`/`newRequestID` + `withRecovery` (+ tests).
- T15: `withRequestLog` + `statusRecorder` + `accessLogLevel` (+ tests).
- T16: `handler.go` loguea cada rama de rechazo con `logDecision` (+ tests).

### Checkpoint B (tras T13–T16)
- [x] Un request exitoso produce una línea `"http request"` con `request_id`.
- [x] 400/413/500/503 loguean motivo y `request_id`; 5xx ⇒ ERROR, 4xx ⇒ WARN.

### Fase 2 — Logging del pipeline
- T17: `WorkerPool` con logger, `Submit(ctx, data)`, `job.requestID` y logs de
  arranque/cola/pjob (+ actualizar tests).
- T18: `queued_extractor` loguea cancel/deadline (+ logger) (+ tests).
- T19: `extractor` con logger y `"panic recovered while extracting"` (+ tests).

### Checkpoint C (tras T17–T19)
- [x] Cola llena ⇒ `WARN "queue full; rejecting job"` correlacionado.
- [x] Extracción completada/fallida loguea `request_id`, `pages`, `duration_ms`.
- [x] Panic en el extractor ⇒ `ERROR` con `stack`, respuesta 500.

### Fase 3 — Apagado seguro + wiring de `main`
- T20: `run`/`runWithSignals`, `logging.New()`, `logStartup`, inyección de
  loggers, logs de arranque/parada.
- T21: drenado acotado (`WorkerPool.CloseContext`), segundo `SIGTERM`,
  `stop_grace_period` en compose.

### Checkpoint D (tras T20–T21)
- [x] `SIGTERM` ⇒ `"shutting down"` + `"worker pool drained"` + salida 0.
- [x] `SHUTDOWN_TIMEOUT` agotado ⇒ salida ≠ 0 y log de error.
- [x] Segundo `SIGTERM` fuerza salida.

### Fase 4 — Docs y verificación
- T22: README (sección Logging + tabla de env), `AGENTS.md`, nota en
  `tasks/todo.md:157`.
- T23: verificación integral (build, tests, docker gate, smoke, precedencia).

### Checkpoint Final
- [x] Todos los AC cumplidos; `make test` + `make test-unit` + `make build` +
  `docker build --target test .` verdes.
- [ ] Revisión con humano antes de cerrar.

## 12. Riesgos y mitigaciones (observabilidad)

| Riesgo | Impacto | Mitigación |
|---|---|---|
| Cambiar firmas (`NewHandler`, `NewWorkerPool`, `NewExtractor`, `Submit`, `Load`) rompe tests | Medio | Tareas de tests incluidas en cada task; `logging.Discard()` como default |
| `request_id` no cruza al worker (ctx anulado) | Medio | Se guarda en el `job` al hacer `Submit(ctx, …)` (T17) |
| Apagado colgado por call nativa no cancelable | Alto | `CloseContext` con deadline + segundo signal + `stop_grace_period > SHUTDOWN_TIMEOUT` |
| `.env` local filtrado a la imagen | Medio | Añadir `.env` a `.dockerignore` (T12) |
| Romper nombres de env documentados (`MAX_UPLOAD_MB`, etc.) | Bajo | Se conservan los nombres; solo se añade validación |
| Divergencia con el orquestador en nombres/estructura | Bajo | Copia verbatim de `logging`/`reqctx`; mismos nombres de middleware y mensajes |

## 13. Preguntas abiertas (con default asumido)

1. **`.env` en host (`go run`/`make run`):** el orquestador no carga `.env` en
   Go (solo Compose). *Default asumido: Compose-only, sin `godotenv`.* Si se
   quiere `make run` levantando `.env`, se agrega `godotenv` con semántica
   no-override (no rompe el default).
2. **Nombres de tamaño:** *default asumido: mantener `MAX_UPLOAD_MB`/
   `QUEUE_MAX_MB` enteros (validados)*. Alternativa: migrar a `parseByteSize`
   (`50MB`) como el orquestador.
3. Confirmar que el endurecimiento del apagado entra completo (T20–T21), no solo
   los logs.

## 14. Comandos de verificación (observabilidad)

| Nivel | Comando |
|---|---|
| Vet + unit | `make test` (`go vet ./... && go test ./...`) |
| Unit con tag | `make test-unit` |
| Build | `make build` |
| Docker gate | `docker build --target test .` |
| Smoke | `LOG_LEVEL=debug ./bin/server` + `curl` (PDF válido / no-PDF / > `MAX_UPLOAD_MB`) |
| Precedencia | `PORT=9090 docker compose ...` vs `.env` (`PORT=8080`) ⇒ gana 9090 |
