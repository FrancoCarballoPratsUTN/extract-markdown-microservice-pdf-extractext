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

> **Decisión humana (vigente).** La observabilidad del servicio es **sólo
> logging**: no hay métricas, no hay tracing, no hay Prometheus ni Grafana.
> El `X-Request-ID` se conserva como identificador de correlación entre logs,
> no como traza distribuida. Ninguna de las decisiones de este documento
> habilita un endpoint de métricas.

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

### 9.1 Estado de implementación (2026-10-21)

Lo que quedó implementado de esta Parte 2, y lo que no:

| Decisión | Estado | Nota |
|---|---|---|
| `internal/logging` + `internal/reqctx` | implementado | tal como se describe |
| Middleware HTTP (`withRequestID`, `withRecovery`, `withRequestLog`, `statusRecorder`, `accessLogLevel`) | implementado | en `internal/presentation/http`, con los nombres del orquestador |
| Logging del pipeline (`WorkerPool.Submit(ctx, data)`, `job.requestID`, `queued_extractor`, `extractor`) | implementado | `request_id` correlaciona la línea de acceso con las del pipeline |
| `config` fail-fast (`Load() (Config, error)`) | implementado, con una diferencia | **no** se inyecta `getenv`; los tests usan `t.Setenv`. La inyección se decidió innecesaria: `os.Getenv` + `t.Setenv` ya aísla los tests |
| `SHUTDOWN_TIMEOUT` + `CloseContext` + `run`/`serve` | implementado | el drenado está acotado por `SHUTDOWN_TIMEOUT`. `serve` recibe el extractor ya construido, así que el apagado se testea sin la librería nativa |
| Segundo `SIGTERM` ⇒ `os.Exit(1)` | **no implementado** | el apagado ya está acotado por `SHUTDOWN_TIMEOUT` y Docker aplica `SIGKILL` tras `stop_grace_period`; un `os.Exit` desde una goroutine no es testeable |
| Higiene del log (sin tokens, cuerpos ni PDF en stdout) | implementado | `internal/logging/sanitize.go`: `Truncate` (valor y stack acotados en longitud) y `MaxValueLength`/`MaxStackLength`. No hay `SafeURL`: este servicio no hace HTTP saliente, así que no hay URL que sanear |

El `SHUTDOWN_TIMEOUT` es un entero en segundos (no una duración), a diferencia
del orquestador, que usa `35s`. `deploy/compose.yaml` ya pasaba `35` al
extract; no se unifican los formatos.

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
- [ ] Segundo `SIGTERM` fuerza salida — no implementado (ver §9.1): el apagado
      ya está acotado y Docker aplica `SIGKILL` tras `stop_grace_period`.

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

## 15. Cierre simple de runtime y CVE

El runtime usa `gcr.io/distroless/cc-debian12:nonroot`, sin shell, gestor de
paquetes ni `wget`. Se eliminó el `HEALTHCHECK` interno basado en BusyBox; las
sondas externas, Traefik y Consul continúan consultando `/healthz`.

La configuración de negocio y límites permanece en `internal/config`. Las
variables de Consul que sólo usa el composition root pueden leerse ahí en
`cmd/server`; no se agrega otra capa de configuración hasta que más paquetes
necesiten esos valores. `LOG_LEVEL` y `LOG_FORMAT` siguen en
`internal/logging`.

# Parte 3 — Correcciones del Code Review para producción

## Objetivo

Resolver los bloqueantes identificados en la revisión del working tree sin
alterar el contrato funcional del extractor:

1. limitar el tamaño total de una petición multipart, no sólo el fichero PDF;
2. proteger con tests el contrato de tags de Consul, donde las comas pueden
   formar parte de un valor;
3. evitar que el runtime Docker ejecute el servidor como root;
4. eliminar estado muerto del recorder HTTP.

## Orden incremental y dependencias

1. **T25 — Límite multipart (TDD):** test de reproducción, implementación y
   mapeo explícito de `http.MaxBytesError`.
2. **T26 — Tags de Consul (TDD):** test de regresión para comas internas y
   separador `;`.
3. **Checkpoint E:** `make test` y `make build` verdes.
4. **T27 — Runtime no-root:** declarar usuario no privilegiado en la imagen y
   verificar el gate Docker.
5. **T28 — Limpieza de recorder:** eliminar `bytes` sin cambiar el contrato de
   logging.
6. **Checkpoint Final de hardening:** tests, build, diff y Docker gate verdes.

## Criterios de aceptación

- Ninguna petición multipart puede superar el límite total configurado aunque
  contenga partes que no sean `file`.
- Un tag como `security-headers@file,extract-cb@file` se conserva como un único
  valor y `;` separa tags distintos.
- El proceso del contenedor runtime no se ejecuta como root.
- `statusRecorder` no conserva datos que no utiliza.
- Cada cambio de comportamiento tiene un test de regresión y las validaciones
  existentes continúan pasando.

## Riesgos y límites

- El overhead multipart forma parte del límite total; el handler debe reservar
  el límite configurado para la request completa y conservar el límite propio
  del PDF.
- No se cambia la imagen base ni se añade tooling adicional en esta fase.
- No se modifica la documentación histórica de `tasks/plan.md`/`tasks/todo.md`.

## Estado de ejecución

- T25 completado: el cuerpo multipart completo queda limitado y se drena para
  detectar exceso aunque el PDF aparezca antes que otras partes.
- T26 completado: las comas internas se preservan y `;` separa tags.
- T27 completado: el runtime declara `USER 65532:65532`.
- T28 completado: se eliminó el contador de bytes no utilizado del recorder.
- Verificado: `make test`, `make build`, `docker build --target test .`,
  construcción de runtime e inspección de usuario efectivo, y `git diff --check`.

# Parte 4 — Correlación de `X-Request-ID`

## Objetivo

Implementar correlación de requests de extremo a extremo para que el servicio
respete el `X-Request-ID` enviado por el orquestador, genere uno seguro cuando
falte, lo devuelva al cliente y lo incluya en los logs HTTP y del pipeline de
extracción. La implementación debe preservar los límites actuales de logging y
no tratar el identificador como una credencial.

## Decisiones de diseño

- Se usará el header `X-Request-ID` existente como contrato de entrada y salida.
- Un header ausente o inválido recibirá un UUID generado por el servicio.
- El valor será validado y acotado antes de entrar al contexto o a los logs.
- El middleware de request ID se ejecutará antes de recovery y access logging.
- El worker pool copiará el ID al `job`, porque el extractor se ejecuta con
  `context.Background()` y no puede recuperar el contexto HTTP original.
- No se crearán métricas, tracing ni una dependencia externa nueva.

## Orden incremental y dependencias

1. **T29 — Contexto y validación del request ID (TDD):** crear el paquete
   `internal/reqctx` con generación, validación, lectura/escritura del ID y
   tests unitarios.
2. **T30 — Middleware HTTP (TDD):** consumir o generar `X-Request-ID`, añadirlo
   al contexto y ecoarlo en la respuesta; cubrir éxito, ausencia e input
   inválido.
3. **Checkpoint G:** tests focalizados, `make test` y `make build` verdes.
4. **T31 — Propagación al pipeline (TDD):** copiar el ID al job y añadirlo a
   access logs, recovery, decisiones del handler y logs del worker pool.
5. **T32 — Verificación de integración:** probar una request completa y asegurar
   que todas las líneas relacionadas comparten el mismo identificador.

## Criterios de aceptación

- Un `X-Request-ID` válido enviado por el orquestador aparece sin cambios en la
  respuesta y en todos los logs relacionados.
- Si falta o no cumple la validación, el servicio genera un ID nuevo y no
  registra el valor inválido sin acotarlo.
- Los logs de access, recovery, rechazo, cola y extracción contienen
  `request_id` cuando existe una request HTTP.
- Health checks conservan su comportamiento actual y no generan access logs.
- Los tests existentes permanecen verdes y no se registran cuerpos PDF, tokens
  ni valores ilimitados.

## Riesgos y mitigaciones

| Riesgo | Impacto | Mitigación |
|---|---|---|
| ID malicioso o excesivamente largo | Medio | Validación estricta y límite de longitud antes de loguear |
| Pérdida del ID al entrar al worker pool | Alto | Copia explícita en `job` y tests de resultado de logs |
| Cambio accidental del orden de middleware | Medio | Test de respuesta y access log con el mismo ID |
| Contrato histórico desactualizado | Bajo | Verificar código real y actualizar documentación sólo después de implementar |

## Verificación prevista

- `go test ./internal/reqctx`
- `go test ./internal/presentation/http`
- `go test ./internal/usecase`

## Estado de ejecución

Parte 4 completada. T29–T32 están implementadas y verificadas con `make test`,
`make build`, `git diff --check` y un smoke test HTTP que confirmó el eco de
`X-Request-ID: orchestrator-123` en la respuesta y su presencia en el access
log.

## Parte 5 — Runtime Distroless

Se reemplazó `debian:bookworm-slim` por
Se evaluó Alpine `3.24.3` (digest
`sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9`),
pero fue descartada: aunque compila, el runtime falla al cargar `pdf_oxide`
porque la biblioteca requiere símbolos `glibc` hasta `GLIBC_2.35` y `gcompat`
no ofrece compatibilidad suficiente. Se conserva
`gcr.io/distroless/base-debian13:nonroot`, fijada al digest
`sha256:9039a88709bcc2bacf5a2faac331d65dd6840f763f225ad9594cc0c67bcd0fba`,
con sólo `libgcc_s.so.1` añadido, sin `wget` ni `HEALTHCHECK` interno.

La decisión queda cerrada: no se aplica Alpine al runtime mientras
`pdf_oxide` se distribuya como biblioteca glibc. Mezclar una glibc externa
sobre Alpine introduciría una base no soportada y una cadena de suministro
menos confiable que la imagen Distroless funcional.

Verificación final:

- `docker build --target test .` ✅
- `docker build --target runtime .` ✅
- Smoke `/healthz` en contenedor Distroless ✅
- Extracción real de PDF con `X-Request-ID` y `pdf_oxide` ✅
- Usuario efectivo `65532:65532` y sin `HEALTHCHECK` interno ✅
- `git diff --check` ✅
- `make test`
- `make build`
- Smoke HTTP con `curl -H 'X-Request-ID: orchestrator-123'` y revisión de logs.

---

# Parte 6 — Plan de correcciones del Code Review (RC-1)

## 1. Objetivo y alcance

Cerrar los hallazgos del informe de Code Review del working tree sin ampliar el
alcance funcional ni añadir dependencias. El plan cubre **3 bloqueantes**, **5
hallazgos no bloqueantes** y **1 sugerencia de hardening aceptada**, y deja
explícitamente fuera lo que no aporta al RC (ver §10).

| ID | Hallazgo | Severidad | Tarea |
|---|---|---|---|
| H1 | Límite multipart: PDF == `MAX_UPLOAD_MB` falla solo en multipart | Bloqueante | T33 |
| H2 | `CONSUL_SERVICE_TAGS` cambia de contrato sin documentar | Bloqueante | T34 |
| H3 | `request_id` se pierde en el panic del extractor; esquema inconsistente | Bloqueante | T36–T38 |
| H4 | `reqctx` sin doc comments, semántica silenciosa, constante muerta | No bloqueante | T35 |
| H5 | Dockerfile: pin inconsistente, rationale eliminado, README no coincide | No bloqueante | T39 |
| H6 | Ramas nuevas sin test (header inválido, rechazo, abandono, drain) | No bloqueante | T33/T37/T38 |
| H7 | Documentación del RC contradictoria | No bloqueante | T40 |
| H8 | `readMultipartPDF` con dos responsabilidades | No bloqueante | T33 |
| S1 | Hardening del contenedor en compose (sugerencia del review §4) | No bloqueante | T41 |

**Regla de oro:** cada cambio de comportamiento nace de un test que falla
(RED), se implementa con el mínimo código que lo hace pasar (GREEN) y se
refactoriza sin cambiar el contrato (REFACTOR). Ningún test existente se
"adapta" para tapar una regresión: si un test cambia de supuesto, el plan lo
justifica por escrito.

## 2. Definition of Done del RC

Ninguna tarea se marca `[x]` si no se cumplen **todas** estas puertas:

- [ ] `make test` verde (`CGO_ENABLED=0 go vet ./... && go test ./...`).
- [ ] `make test-unit` verde (`go test -tags unit ./...`).
- [ ] `make build` verde.
- [ ] `docker build --target test .` verde.
- [ ] `docker build --target runtime .` verde + smoke `/healthz` + extracción real.
- [ ] `docker compose config` válido y el servicio endurecido corre con
  `read_only`, `cap_drop: ALL` y `no-new-privileges` (T41).
- [ ] `gofmt -l .` sin salida y `git diff --check` limpio.
- [ ] `git diff go.mod go.sum` vacío: **cero dependencias nuevas**.
- [ ] Cada comportamiento nuevo tiene un test que falla sin el cambio (evidencia RED en la descripción del PR).
- [ ] Las ramas nuevas quedan cubiertas: `go tool cover -func` sin líneas nuevas sin ejecutar.
- [ ] Toda función exportada nueva tiene doc comment (convención del repo).
- [ ] Los docs describen una sola verdad (imagen, límites, tags, `X-Request-ID`).

## 3. Decisiones de diseño

- **D1 — Dos límites explícitos, no uno reutilizado.** El cuerpo multipart y la
  parte PDF son cosas distintas: `maxBodyBytes = maxBytes + multipartOverheadAllowance`
  y `maxPartBytes = maxBytes`. El allowance cubre boundaries y cabeceras de
  parte; es una **constante interna**, no configuración (YAGNI).
- **D2 — `;` es el único separador de tags.** No se puede hacer *fail-fast* ante
  comas porque un tag con comas y sin `;` es un caso **válido** (lista de
  middlewares de Traefik). La mitigación es documentación + nota de release.
- **D3 — Un único punto escribe `request_id`.** `logging.WithRequestID(ctx, logger)`
  es la única función que añade el campo. Elimina las 4 variantes actuales y
  fija el esquema: **el campo se omite cuando no hay ID**, nunca `""`.
- **D4 — `reqctx` sigue siendo un paquete hoja.** No importa otros paquetes
  `internal/*`; `internal/logging` sí puede importarlo (sin ciclo). No se
  renombra en este RC: el coste de churn no aporta valor (YAGNI).
- **D5 — Se mantiene `base-debian13` en este RC.** Es la imagen ya verificada con
  smoke real; migrar a `cc-debian13:nonroot` invalidaría esa verificación y
  exige re-smoke. Queda como backlog (§10), documentando el motivo de la copia
  manual de `libgcc_s.so.1`.
- **D6 — El ID generado pasa a `crypto/rand.Text()`.** Sustituye el UUIDv4
  artesanal (12 líneas con `encoding/hex` y manipulación de bits) por una línea
  de la stdlib. `rand.Text()` devuelve **26 caracteres** del alfabeto base32
  RFC 4648 (`A-Z`, `2-7`) con **al menos 128 bits** de aleatoriedad: colisiones
  despreciables y sin necesidad de coordinación entre réplicas, así que dos
  peticiones simultáneas no pueden compartir ID. Se acepta el único coste real:
  deja de ser un UUID con formato reconocible, por lo que el README **debe**
  dejar de prometer "UUID" y el tooling que auto-detecta UUIDs ya no lo verá
  (ver §8). El contrato de validación (`IsValid`: ASCII imprimible ≤ 128) no
  cambia, así que un ID de orquestador externo sigue siendo válido.
- **D7 — Hardening del contenedor en `docker-compose.yml` entra en este RC.**
  El servicio no escribe en disco ni necesita privilegios, así que se añaden
  `read_only: true`, `security_opt: ["no-new-privileges:true"]` y
  `cap_drop: ["ALL"]`. `tmpfs: /tmp` **solo** si el smoke demuestra que
  `libpdf_oxide` (vía `purego`/`dlopen`) necesita escribir en `/tmp`; se añade
  como consecuencia de una prueba, no por precaución (YAGNI). El resto de
  servicios del compose no se toca.

## 4. Grafo de dependencias y secuencia

```
T33 (payload)  ─┐
T34 (tags)     ─┼─► Checkpoint H
T35 (reqctx)   ─┴─► T36 (helper) ─► T37 (extractor) ─► T38 (tests) ─► Checkpoint I/J
T39 (docker)   ───────────────────────────────────────────────────► Checkpoint K
T41 (compose)  ───────────────────────────────────────────────────► Checkpoint K
T40 (docs)     ◄── depende del estado final de T33…T41 ───────────► Checkpoint Final
```

| Orden | Tarea | Depende de | Scope | Riesgo |
|---|---|---|---|---|
| 1 | T33 Límites multipart | — | S/M | Medio |
| 2 | T34 Tags de Consul | — | S | Bajo |
| 3 | T35 `reqctx` endurecido | — | S | Bajo |
| 4 | T36 Helper `logging.WithRequestID` | T35 | M | Bajo |
| 5 | T37 Panic del extractor | T36 | S | Bajo |
| 6 | T38 Tests residuales de correlación | T36, T37 | S | Bajo |
| 7 | T39 Dockerfile + README | — | S | Bajo |
| 8 | T41 Hardening del contenedor en compose | T39 | S | Medio |
| 9 | T40 Coherencia documental | T33…T41 | S | Bajo |

## 5. Tareas

### [x] T33 — Límites de payload explícitos (parte vs cuerpo)

**Descripción:** hoy `readMultipartPDF` aplica el mismo `maxBytes` al cuerpo
completo y a la parte PDF, así que un PDF de exactamente `MAX_UPLOAD_MB` pasa
como `application/pdf` y falla como `multipart/form-data`. Se separan los dos
topes y se aprovecha para dividir la función en dos responsabilidades (H8).

**Acceptance criteria:**
- [x] RED: `TestReadPDFPayloadAcceptsMultipartPDFExactlyAtTheLimit` falla con el
  código actual (hoy devuelve `ErrPayloadTooLarge`).
- [x] `readPDFPayload` pasa `maxPartBytes = maxBytes` y
  `maxBodyBytes = maxBytes + multipartOverheadAllowance`.
- [x] `readMultipartPDF(r, maxPartBytes, maxBodyBytes)` documenta qué acota cada
  parámetro y por qué `http.MaxBytesReader(nil, …)` es seguro (comentario).
- [x] `TestReadPDFPayloadRejectsMultipartPDFAboveTheLimit` (PDF = `maxBytes+1`)
  devuelve `ErrPayloadTooLarge` por el límite **de parte**.
- [x] `TestReadPDFPayloadRejectsMultipartBodyAboveTheLimit` (campo no-`file`
  posterior al documento que hace que el cuerpo supere `maxBytes+allowance`)
  devuelve `ErrPayloadTooLarge` por el límite **de cuerpo** (drain).
- [x] `TestReadPDFPayloadRejectsMultipartBodyAboveTheLimitBeforeTheFile` cubre el
  mismo tope cuando el exceso se detecta en `NextPart` (campo gigante antes del
  documento) y no debe degradarse a `ErrMissingFile`.
- [x] Se **reescribe** `TestReadPDFPayloadRejectsMultipartBodyExceedingMaxBytes`:
  su supuesto (un cuerpo de ~200 B con `maxBytes=32` excede el tope) deja de ser
  cierto al añadir el allowance. El nuevo test ataca el tope de cuerpo, no el de
  parte.
- [x] `TestReadPDFPayloadReportsATruncatedMultipartBody` (drain con reader roto)
  devuelve `ErrUnreadableBody`.
- [x] SRP: `readFilePart(reader, maxPartBytes)` y
  `ensureBodyConsumed(body)` tienen una sola razón de cambio y ≤ 20 líneas cada
  una; `readMultipartPDF` queda ≤ 15 líneas como orquestador.
- [x] No se añade ninguna variable de entorno ni parámetro configurable (YAGNI).
- [x] Cobertura medida: todas las funciones de `payload.go` al 100%
  (`go tool cover -func`).

**Refactor de referencia:**
```go
const (
	pdfFormField = "file"

	// multipartOverheadAllowance cubre boundaries, cabeceras de parte y el
	// delimitador de cierre. Sin él, un PDF de exactamente maxBytes se
	// rechazaría solo por el envoltorio multipart.
	multipartOverheadAllowance = 8 << 10
)

func readPDFPayload(r *http.Request, maxBytes int64) ([]byte, error) {
	// ...
	case "multipart/form-data":
		return readMultipartPDF(r, maxBytes, maxBytes+multipartOverheadAllowance)
	// ...
}

// readMultipartPDF extrae la parte "file" acotada por maxPartBytes y exige que
// el cuerpo completo no supere maxBodyBytes.
func readMultipartPDF(r *http.Request, maxPartBytes, maxBodyBytes int64) ([]byte, error) {
	// nil es seguro: MaxBytesReader solo hace un type assertion sobre el writer
	// para avisar a net/http; el 413 lo escribe el handler.
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, ErrMissingFile
	}
	data, err := readFilePart(reader, maxPartBytes)
	if err != nil {
		return nil, err
	}
	if err := ensureBodyConsumed(r.Body); err != nil {
		return nil, err
	}
	return data, nil
}
```

**Verification:**
- [x] RED/GREEN: `CGO_ENABLED=0 go test ./internal/presentation/http -run 'TestReadPDFPayload'`
- [x] Handler: `CGO_ENABLED=0 go test -tags unit ./internal/presentation/http -run 'TestExtractHandler'`
- [x] `make test` y `make build`.
- [x] `make test-unit` (un fallo intermitente previo de `TestServeAnswersTheHealthProbe`
  en `cmd/server` desapareció al re-ejecutar; no toca `presentation/http`).
- [x] `gofmt -l .` sin salida y `git diff --check` limpio.
- [x] `docker build --target test .` verde (vet + tests + unit-tagged +
  integration dentro de la imagen).

**Resultado (2026-11-04):**
- Implementado con los dos topes (`maxPartBytes = maxBytes`,
  `maxBodyBytes = maxBytes + 8 KiB`) y las tres funciones
  `readMultipartPDF` / `readFilePart` / `ensureBodyConsumed`.
- El RED descubrió un **bug adicional**: un PDF multipart por encima del límite
  devolvía `ErrUnreadableBody` (400 "could not read request body") en vez de
  `ErrPayloadTooLarge` (413). Queda cubierto por
  `TestExtractHandlerMultipartPDFAboveTheLimitReturns413`.
- La rama `isPayloadTooLarge` que se añadió a `readLimited` se **eliminó** por
  inalcanzable: con `maxPartBytes+1 < maxBodyBytes` el `MaxBytesReader` del
  cuerpo no puede dispararse leyendo la parte. No se deja código muerto.
- Tests añadidos: frontera (PDF == límite, payload y handler), tope de parte,
  tope de cuerpo por drain y por `NextPart`, drain truncado, `Content-Type`
  malformado y multipart sin boundary.

**Dependencies:** ninguna.
**Files likely touched:** `internal/presentation/http/payload.go`,
`internal/presentation/http/payload_test.go`,
`internal/presentation/http/handler_test.go`.
**Estimated scope:** S/M.

**Principios:** **SRP** (extrae dos funciones con una razón de cambio cada una),
**DRY** (un solo lugar define cada tope), **KISS** (constante interna, sin
configuración nueva), **YAGNI** (no se añade un flag para el allowance).

---

### [x] T34 — Contrato de tags de Consul: helper + documentación

**Descripción:** el PR deja de aceptar `,` como separador. Es un cambio
incompatible y hoy solo está explicado en un comentario de código. Se extrae la
función, se cubre con tabla de casos y se documenta el contrato y la migración.

**Acceptance criteria:**
- [x] `splitServiceTags(raw string) []string` extraída de `serviceRegistration`,
  con doc comment que explica por qué la coma **no** es separador.
- [x] `TestSplitServiceTags` (tabla) cubre: `""` → `nil`; `"metrics"` →
  `["metrics"]`; `"a,b"` → `["a,b"]`; `"a;b"` → `["a","b"]`; `" a ; ; b "` →
  `["a","b"]`; `"a;b,c"` → `["a","b,c"]`.
- [x] `TestServiceRegistrationPreservesCommasInsideTags` sigue verde sin
  cambios (es el test de contrato del composition root).
- [x] `README.md` documenta `CONSUL_SERVICE_TAGS` (separador `;`, la coma es
  parte del valor) y, si ya existen en el código, el resto de `CONSUL_*`.
- [x] Tras `RegisterService` con éxito, `main.go` emite **un log de arranque con
  los tags efectivos** (`"registered in Consul"` o similar, atributo `tags`,
  nivel `info`) para que un operador detecte en el primer arranque si una config
  con comas se degradó a un único tag. El log usa el logger ya construido, no
  `fmt.Print*`.
- [x] `TestServiceRegistrationLogsTheEffectiveTags` verifica que el log contiene
  los tags **después** del split (no el valor crudo de la variable).
- [x] Nota de release en este plan, redactada como contrato operativo:
  *"`CONSUL_SERVICE_TAGS` usa `;` como separador desde este release. La coma se
  conserva dentro de un tag (necesario para `security-headers@file,extract-cb@file`
  de Traefik). Si tu entorno usaba comas como separador, migra a `;`: tras
  actualizar, revisa el log de registro de Consul (`tags`) para confirmar los
  tags efectivos."* **No** se hace fail-fast: un tag con comas y sin `;` es
  válido y no se puede distinguir de una config antigua (decisión del usuario:
  no hay inventario de entornos).
- [x] No se añade configuración ni dependencia (YAGNI).

**Verification:**
- [x] RED/GREEN: `CGO_ENABLED=0 go test ./cmd/server -run 'TestSplitServiceTags|TestServiceRegistration'`
- [x] `make test`.

**Dependencies:** ninguna.
**Files likely touched:** `cmd/server/main.go`, `cmd/server/main_test.go`,
`README.md`.
**Estimated scope:** S.

**Principios:** **SRP** (parsing separado del composition root), **Clean Code**
(función nombrada y testeable en vez de un `for` inline), **DRY** (una sola
definición del separador), **KISS** (sin compatibilidad bidireccional ambigua).

---

### [x] T35 — Endurecer `internal/reqctx`

**Descripción:** la API pública no está documentada, `WithRequestID` se comporta
distinto de `EnsureRequestID` sin decirlo, hay una constante que solo usa el
test y el generador de ID es un UUIDv4 artesanal de 12 líneas que la stdlib ya
resuelve en una (`crypto/rand.Text()`, Go ≥ 1.24; `go.mod` es 1.26). Se fija el
contrato antes de propagarlo (T36+).

**Acceptance criteria:**
- [x] Doc comment en `EnsureRequestID`, `WithRequestID`, `RequestID`, `IsValid`
  y `newRequestID` (convención del repo).
- [x] `WithRequestID` normaliza con `strings.TrimSpace`, igual que
  `EnsureRequestID`; un ID inválido sigue devolviendo el contexto sin ID, pero
  ahora está **documentado** y testeado.
- [x] `TestWithRequestID` cubre válido, con espacios exteriores, inválido y
  vacío.
- [x] `TestIsValid` cubre el borde exacto: 128 caracteres válidos, 129
  inválidos; control (`\n`, `\t`), espacio y no-ASCII.
- [x] `newRequestID` es **una línea**: `return rand.Text()` (D6). Se eliminan
  `encoding/hex`, el array de 16 bytes y la manipulación de los bits de versión
  y variante.
- [x] `generatedIDLength` se elimina de producción. El test define su propia
  constante `wantGeneratedIDLength = 26` (longitud actual de `rand.Text()`),
  documentada como **contrato mínimo**, no como formato estable.
- [x] `TestEnsureRequestIDGeneratesValueWhenMissing` valida: longitud 26,
  alfabeto base32 RFC 4648 (`A-Z`, `2-7`), que `IsValid` lo acepta y que dos
  llamadas generan valores **distintos**.
- [x] `TestNewRequestIDIsUniqueAcrossCalls` genera un lote (p. ej. 1000) y
  comprueba que no hay colisiones: evidencia de que dos peticiones no pueden
  confundirse.
- [x] `README.md` deja de prometer un **UUID**: la sección de `X-Request-ID`
  describe "un identificador aleatorio opaco (26 caracteres base32) cuando el
  cliente no lo envía". El README es parte del contrato de este release.
- [x] Sin helpers nuevos (`MustParse`, `Parse`) ni opciones: YAGNI.

**Refactor de referencia:**
```go
// newRequestID genera un identificador de correlación. rand.Text() devuelve 26
// caracteres base32 con al menos 128 bits de aleatoriedad (Go ≥ 1.24): no
// puede fallar (paniquea si la fuente del sistema falla) y no necesita
// coordinación entre réplicas, así que dos peticiones no pueden compartir ID.
func newRequestID() string { return rand.Text() }
```

**Verification:**
- [x] RED/GREEN: `CGO_ENABLED=0 go test ./internal/reqctx -v`
- [x] Auditoría: `grep -rn 'hex.EncodeToString\|generatedIDLength\|randomBytes'
  --include=*.go internal/` → 0 en producción.
- [x] `make test`.

**Dependencies:** ninguna.
**Files likely touched:** `internal/reqctx/reqctx.go`,
`internal/reqctx/reqctx_test.go`, `README.md`.
**Estimated scope:** S.

**Principios:** **Clean Code** (API autoexplicativa, sin fallos silenciosos no
documentados), **KISS** (una línea de stdlib en vez de 12 de bit-twiddling),
**YAGNI** (no se añade superficie pública ni dependencia).

---

### [ ] T36 — Helper único `logging.WithRequestID` y refactor de call sites

**Descripción:** el campo `request_id` se añade hoy de cuatro maneras distintas
(`logger.With`, append condicional, atributo siempre presente con `""`). Se crea
un helper y se sustituyen todos los puntos para que el esquema sea uno solo.

**Acceptance criteria:**
- [ ] Nuevo `internal/logging/requestid.go`:
  ```go
  // WithRequestID devuelve un logger que añade request_id cuando el contexto lo
  // trae. Es el único lugar que decide cómo se escribe el campo, para que todas
  // las líneas de una petición compartan el mismo esquema.
  func WithRequestID(ctx context.Context, logger *slog.Logger) *slog.Logger
  ```
- [ ] `TestWithRequestIDAddsTheField` y `TestWithRequestIDWithoutIDLeavesTheLogUnchanged`.
- [ ] Se sustituyen los 5 call sites: `middleware.go` (recovery y access log),
  `logging.go` (`logDecision`), `workerpool.go` (`run` y `logRejection`) y
  `queued_extractor.go` (abandono).
- [ ] `logDecision` vuelve a reservar `len(attrs)+2` (con el helper deja de
  reasignar en cada rechazo).
- [ ] Esquema único: el campo se **omite** cuando no hay ID; nunca `""`.
- [ ] Auditoría DRY: `grep -rn '"request_id"' --include=*.go internal/ | grep -v _test.go`
  devuelve **exactamente 1** línea (el helper).
- [ ] Sin ciclo de imports: `internal/logging` importa `internal/reqctx` (hoja),
  y ningún paquete de `internal/reqctx`.
- [ ] `make test` + `make test-unit` verdes (el refactor no cambia ningún log
  observable salvo el `""` que desaparece).

**Verification:**
- [ ] RED/GREEN: `CGO_ENABLED=0 go test ./internal/logging ./internal/presentation/http ./internal/usecase`
- [ ] Auditoría: `grep -rn '"request_id"' --include=*.go internal/ | grep -v _test.go`
- [ ] `make test`.

**Dependencies:** T35 (contrato de `reqctx`).
**Files likely touched:** `internal/logging/requestid.go` (nuevo),
`internal/logging/requestid_test.go` (nuevo),
`internal/presentation/http/middleware.go`,
`internal/presentation/http/logging.go`, `internal/usecase/workerpool.go`,
`internal/usecase/queued_extractor.go`.
**Estimated scope:** M.

**Principios:** **DRY** (una sola implementación), **SRP** (el helper solo
decide el campo), **OCP** (añadir correlación a un log nuevo es usar el helper,
no copiar el condicional), **KISS** (sin abstracción de "contexto de logging"
con una sola implementación).

---

### [ ] T37 — Correlación del `panic` del extractor

**Descripción:** el pool pasa un contexto con el ID, pero `extractor.log(ctx)`
lo ignora, así que `"panic recovered while extracting"` —el log más útil para
diagnosticar un 500— sale sin `request_id`. Es el AC de T31 que hoy no se
cumple.

**Acceptance criteria:**
- [ ] RED: `TestPanicLogCarriesRequestID` falla hoy. Usa un `fakeDocument` cuyo
  `ToMarkdownAll` paniquea, un logger a buffer y
  `reqctx.WithRequestID(context.Background(), "orchestrator-123")`.
- [ ] GREEN: `extractor.log(ctx)` devuelve
  `logging.WithRequestID(ctx, logger)`; el log contiene
  `"request_id":"orchestrator-123"`.
- [ ] El mismo test sigue comprobando `panic` y `stack` (no se pierde el
  diagnóstico ni el truncado de `logging.Truncate`).
- [ ] Sin cambios en el contrato de `Extractor` ni en el pool (el contexto ya
  llevaba el ID).
- [ ] `make test` verde.

**Verification:**
- [ ] RED/GREEN: `CGO_ENABLED=0 go test ./internal/infrastructure/pdf -run 'TestPanic'`
- [ ] `make test`.

**Dependencies:** T36.
**Files likely touched:** `internal/infrastructure/pdf/extractor.go`,
`internal/infrastructure/pdf/extractor_test.go`.
**Estimated scope:** S.

**Principios:** **DRY** (usa el helper, no otro condicional), **Clean Code** (el
`ctx` deja de ser un parámetro decorativo), **KISS** (una línea de cambio).

---

### [ ] T38 — Tests residuales de correlación

**Descripción:** cubrir las ramas nuevas que hoy no se ejecutan y el AC de T30
que no tiene test ("un ID inválido no se propaga ni se registra").

**Acceptance criteria:**
- [ ] `TestWithRequestIDReplacesAnInvalidHeader` (tabla): `"bad id"` (espacio),
  `"a\nb"` (control), 129 caracteres. En los tres: el header de respuesta es un
  ID generado, el contexto no conserva el valor inválido y el valor crudo **no**
  aparece en los logs.
- [ ] `TestWorkerPoolLogsTheRequestIDWhenTheQueueIsFull` cubre la rama con ID de
  `logRejection` (hoy `logRejection` está al 80%).
- [ ] `TestQueuedExtractorLogsTheRequestIDWhenTheRequestIsAbandoned` cubre la
  rama con ID de `queued_extractor.go` (hoy sin test: `grep abandoned` en tests
  devuelve 0).
- [ ] `go tool cover -func` confirma que las ramas nuevas de `middleware.go`,
  `workerpool.go`, `queued_extractor.go` y `payload.go` quedan ejecutadas.
- [ ] No se duplica el helper de tests existente (`newRecordingLogger`,
  `decodeLogLine`): se reutiliza.

**Verification:**
- [ ] `CGO_ENABLED=0 go test ./internal/presentation/http ./internal/usecase`
- [ ] `CGO_ENABLED=0 go test -coverprofile=/tmp/cover.out ./internal/... && CGO_ENABLED=0 go tool cover -func=/tmp/cover.out | grep -E 'middleware|workerpool|queued|payload'`
- [ ] `make test`.

**Dependencies:** T36, T37.
**Files likely touched:**
`internal/presentation/http/accesslog_recovery_test.go`,
`internal/usecase/workerpool_logging_test.go`,
`internal/usecase/queued_extractor_test.go`.
**Estimated scope:** S.

**Principios:** **Clean Code** (F.I.R.S.T.: reutiliza helpers, sin tests
duplicados), **DRY** (una tabla para los tres casos inválidos).

---

### [ ] T39 — Dockerfile consistente y README alineado

**Descripción:** el runtime se cambió de `cc-debian12:nonroot` a
`base-debian13@digest` + copia manual de `libgcc_s.so.1`. Funciona, pero el pin
es inconsistente entre stages, el README afirma `:nonroot` y el `Dockerfile` no
lo dice, y se eliminaron los comentarios que explican decisiones no obvias.

**Acceptance criteria:**
- [ ] Ambos stages de Go pinneados al mismo patch
  (`golang:1.26.10-bookworm` en `oxide` y `builder`).
- [ ] Comentarios restaurados: por qué `CGO_ENABLED=0` es `ENV` (el target `test`
  lo hereda), por qué se copia `libgcc_s.so.1`, y el gate
  `docker build --target test .`.
- [ ] `README.md` coincide con el `Dockerfile`: no se afirma `:nonroot` si el
  digest no lo garantiza; se documenta que `USER 65532:65532` es la garantía
  efectiva.
- [ ] Decisión D5 registrada: se mantiene `base-debian13` (verificado) y
  `cc-debian13:nonroot` queda como backlog (§10).
- [ ] `docker inspect --format '{{.Config.User}}' <imagen>` devuelve `65532:65532`.
- [ ] `docker build --target test .` y `docker build --target runtime .` verdes.
- [ ] Smoke: `/healthz` responde `ok` y una extracción real con
  `X-Request-ID: orchestrator-123` devuelve el header y aparece en el access log.
- [ ] Sin cambios de comportamiento en la aplicación.

**Verification:**
- [ ] `docker build --target test .`
- [ ] `docker build --target runtime .`
- [ ] `docker run --rm -p 8080:8080 <imagen>` + `curl /healthz` + `curl` de un PDF real.
- [ ] `docker inspect` del usuario efectivo.

**Dependencies:** ninguna.
**Files likely touched:** `Dockerfile`, `README.md`.
**Estimated scope:** S.

**Principios:** **KISS** (no se migra de base sin necesidad), **DRY** (un solo
pin de Go), **Clean Code** (rationale donde no es obvio), **YAGNI** (el cambio a
`cc` queda fuera del RC).

---

### [ ] T40 — Coherencia documental del RC

**Descripción:** el plan y el todo arrastran contradicciones que son la
evidencia de release: `cc-debian12` vs `base-debian13`, una frase truncada, un
riesgo que dice que no se cambia la imagen, y checkpoints sin marcar.

**Acceptance criteria:**
- [ ] §15 (`plan-markdown.md:327`) deja de decir `cc-debian12:nonroot`; una sola
  imagen runtime en todo el documento.
- [ ] Frase truncada de `plan-markdown.md:462` completada ("Se reemplazó X por Y").
- [ ] `plan-markdown.md:379` ("No se cambia la imagen base") corregido o eliminado.
- [ ] `todo-markdown.md:594` (Checkpoint G) marcado o justificado; `T24` cerrada
  como superada por la Parte 5/6.
- [ ] `README.md` sin afirmaciones que el `Dockerfile` no respalde.
- [ ] `README.md` describe `MAX_UPLOAD_MB` como **tope del documento PDF**, no
  del cuerpo: desde T33 el sobre multipart puede ocupar hasta
  `MAX_UPLOAD_MB + 8 KiB` y el PDF sigue acotado por `MAX_UPLOAD_MB`.
- [ ] Tabla de `CONSUL_*` presente (de T34) y coherente con el código.
- [ ] `README.md` y `docs/` **no** prometen un UUID para `X-Request-ID`; describen
  el identificador generado como opaco (D6).
- [ ] `git diff --check` limpio y `grep -n "cc-debian12" tasks/ README.md` sin
  resultados (salvo mención histórica explícita, si se decide conservarla).
- [ ] `grep -rni "uuid" README.md docs/ tasks/plan-markdown.md` sin promesas
  vigentes: solo se admite una mención histórica explícita ("antes se generaba un
  UUIDv4"), nunca como contrato actual.

**Verification:**
- [ ] `grep -rn "cc-debian12\|No se cambia la imagen base" tasks/ README.md`
- [ ] `grep -rni "uuid" README.md docs/`
- [ ] `git diff --check`
- [ ] Lectura cruzada README ↔ Dockerfile ↔ plan.

**Dependencies:** T33, T34, T39, T41 (describen el estado final).
**Files likely touched:** `tasks/plan-markdown.md`, `tasks/todo-markdown.md`,
`README.md`, `docs/technical-guide.md`.
**Estimated scope:** S.

**Principios:** **Clean Code** (documentación sin contradicciones), **DRY** (una
sola fuente de verdad para imagen, límites, tags y formato de ID).

---

### [ ] T41 — Hardening del contenedor en `docker-compose.yml`

**Descripción:** el runtime ya es distroless y no-root (`USER 65532:65532`), no
escribe en disco y no necesita privilegios, pero el compose no lo declara. Se
añade el hardening mínimo al servicio `app` para que el contrato esté en el
despliegue y no solo en la imagen. Entra en este RC (D7).

**Acceptance criteria:**
- [ ] El servicio `app` declara `read_only: true`.
- [ ] El servicio `app` declara `security_opt: ["no-new-privileges:true"]`.
- [ ] El servicio `app` declara `cap_drop: ["ALL"]`.
- [ ] `tmpfs: /tmp` **solo** si el smoke con PDF real falla por escritura en
  `/tmp`; en ese caso se documenta el motivo en un comentario del compose. Si el
  smoke pasa sin `tmpfs`, **no** se añade (YAGNI).
- [ ] No se endurecen `traefik` ni el resto de servicios: monta el socket de
  Docker y necesita capacidades; el alcance es el servicio `app`.
- [ ] `docker compose config` valida sin errores y sin warnings de claves
  desconocidas.
- [ ] `docker inspect` del contenedor en ejecución confirma
  `HostConfig.ReadonlyRootfs=true`, `HostConfig.SecurityOpt` con
  `no-new-privileges` y `HostConfig.CapDrop=["ALL"]`.
- [ ] Smoke end-to-end: `/healthz` responde `ok` y una extracción real de PDF
  devuelve Markdown; los logs no muestran errores de escritura.
- [ ] El `docker-compose.traefik.yml` sigue funcionando por *merge*
  (`docker compose -f docker-compose.yml -f docker-compose.traefik.yml config`).
- [ ] Sin cambios en `Dockerfile`, en código Go ni en variables de entorno.

**Verification:**
- [ ] `docker compose config`
- [ ] `docker compose up -d --build` + `curl /healthz` + `curl` de un PDF real.
- [ ] `docker inspect --format '{{json .HostConfig}}' <contenedor>` y comprobar
  `ReadonlyRootfs`, `SecurityOpt`, `CapDrop`.
- [ ] `docker compose down`.

**Dependencies:** T39 (la imagen es la que se endurece).
**Files likely touched:** `docker-compose.yml`, `README.md` (si se documenta el
hardening).
**Estimated scope:** S.

**Principios:** **KISS** (tres claves estándar, sin perfiles ni overrides
nuevos), **YAGNI** (`tmpfs` solo si una prueba lo exige), **Clean Code**
(comentario con el motivo cuando una clave no es obvia). Seguridad: la imagen ya
es no-root; el compose elimina la superficie restante del contenedor.

---

## 6. Checkpoints

### Checkpoint H — Payload y contrato de entrada
- [ ] T33 y T34 completadas.
- [ ] `make test` + `make test-unit` + `make build` verdes.
- [ ] Test de frontera (PDF == `MAX_UPLOAD_MB` en multipart) en verde.
- [ ] Revisión con humano antes de tocar la correlación.

### Checkpoint I — Contrato de correlación
- [ ] T35 y T36 completadas.
- [ ] Auditoría DRY de `request_id` devuelve 1 línea.
- [ ] `make test` verde; ningún log cambia de esquema salvo la desaparición del `""`.

### Checkpoint J — Correlación completa
- [ ] T37 y T38 completadas.
- [ ] El panic del extractor lleva `request_id`.
- [ ] Las ramas nuevas quedan cubiertas (`go tool cover -func`).

### Checkpoint K — Runtime
- [ ] T39 y T41 completadas.
- [ ] `docker build --target test .` y `--target runtime .` verdes.
- [ ] `docker compose config` válido.
- [ ] Smoke `/healthz` + extracción real + usuario `65532:65532`.
- [ ] Contenedor `app` con `read_only`, `cap_drop: ALL` y `no-new-privileges`
  confirmados con `docker inspect`.

### Checkpoint Final RC
- [ ] T40 completada.
- [ ] Definition of Done (§2) completa.
- [ ] Revisión con humano antes de aprobar el merge.

## 7. Puertas de principio (verificación ejecutable)

| Principio | Puerta | Comando / criterio |
|---|---|---|
| **DRY** | Un solo `request_id` | `grep -rn '"request_id"' --include=*.go internal/ \| grep -v _test.go` → 1 |
| **DRY** | Un solo separador de tags | `grep -rn "Split(.*CONSUL_SERVICE_TAGS" --include=*.go .` → 0 (vive en `splitServiceTags`) |
| **SRP** | Funciones nuevas ≤ 20 líneas | revisión + `gofmt`; `readFilePart`/`ensureBodyConsumed`/`splitServiceTags`/`WithRequestID` |
| **Clean** | API documentada | `go doc ./internal/reqctx ./internal/logging` sin funciones exportadas sin comentario |
| **Clean** | Formato y vet | `gofmt -l .` vacío; `go vet ./...` limpio |
| **SOLID-D** | `reqctx` hoja | `go list -f '{{join .Imports "\n"}}' ./internal/reqctx` → solo stdlib |
| **SOLID-S** | Sin paquetes con dos razones de cambio | `payload.go` separa lectura de parte y validación de cuerpo |
| **KISS** | Cero dependencias nuevas | `git diff go.mod go.sum` vacío |
| **KISS** | Cero configuración nueva | `git diff internal/config` vacío |
| **KISS** | Generación de ID sin bit-twiddling | `grep -rn 'hex.EncodeToString\|generatedIDLength\|randomBytes' --include=*.go internal/ \| grep -v _test.go` → 0 |
| **YAGNI** | Sin abstracciones de un solo uso | no se añaden interfaces, flags ni helpers no usados por ≥ 2 call sites |
| **YAGNI** | `tmpfs` solo con evidencia | `grep -n "tmpfs" docker-compose.yml` → 0 salvo que el smoke lo exija |
| **Seguridad** | Contenedor sin privilegios | `docker inspect` → `ReadonlyRootfs=true`, `CapDrop=["ALL"]`, `no-new-privileges` |
| **Tests** | RED antes de GREEN | cada tarea de comportamiento cita el test que falla sin el cambio |

## 8. Riesgos y mitigaciones

| Riesgo | Impacto | Mitigación |
|---|---|---|
| El allowance multipart (8 KiB) se queda corto con boundaries largos | Bajo | El allowance es holgado (8 KiB vs ~300 B reales); test de frontera con boundary estándar y uno largo |
| Un test existente cambia de supuesto (T33) | Medio | Se reescribe con justificación explícita; no se borra cobertura, se reubica en el tope correcto |
| Entornos con tags separados por comas | Medio | Nota de release + README; el cambio de comportamiento es intencional (D2) |
| `logging` importa `reqctx` y alguien lo invierte | Bajo | `reqctx` sigue siendo hoja; la puerta SOLID-D lo verifica |
| El gate Docker no se puede ejecutar en local/CI sin red | Medio | Se documenta como requisito del checkpoint K; no se marca el RC como verificado sin él |
| Cambiar el esquema de logs (desaparece `request_id: ""`) rompe un dashboard | Bajo | Es una mejora de contrato; se anota en la nota de release |
| `rand.Text()` deja de ser un UUID reconocible y un colector que detecte UUIDs ya no enlaza el ID | Bajo | Es opaco y válido igual (26 chars base32); se documenta en README y nota de release. Si un dashboard depende del formato UUID, se adapta el parser, no el servicio (D6) |
| `read_only: true` rompe si `libpdf_oxide`/`purego` escriben en `/tmp` | Medio | Smoke con PDF real antes de cerrar K; si falla, `tmpfs: /tmp` documentado. La prueba decide, no la precaución (D7) |
| `cap_drop: ALL` impide alguna operación legítima del runtime | Bajo | El servicio no usa puertos privilegiados, ni raw sockets, ni chown; el smoke lo confirma |

## 9. Estado de ejecución

- [x] T33 — completada (2026-11-04); `docker build --target test .` verde
- [x] T34 — completada (2026-11-04); helper, log efectivo, README y nota de release
- [ ] T35 — pendiente
- [ ] T36 — pendiente
- [ ] T37 — pendiente
- [ ] T38 — pendiente
- [ ] T39 — pendiente
- [ ] T40 — pendiente
- [ ] T41 — pendiente

## 10. Fuera de alcance (YAGNI / backlog)

Se registran para que no se reabran sin decisión explícita:

- **Migrar a `gcr.io/distroless/cc-debian13:nonroot`** para eliminar la copia
  manual de `libgcc_s.so.1`. Requiere verificar digest y re-ejecutar el smoke;
  no aporta al RC y invalidaría la verificación actual (D5).
- **`request_id` dentro del `problem+json`** (campo `instance` de RFC 9457). El
  cliente ya recibe el header `X-Request-ID`; añadirlo al body no tiene
  requisito asociado.
- **Error específico para multipart malformado** (hoy `ErrMissingFile`). Cambia
  un mensaje sin impacto operativo; el status ya es 400.
- **Hacer configurable el allowance multipart.** Es un detalle de
  implementación; una variable de entorno más es superficie sin demanda.
- **Métricas, tracing o endpoint de telemetría.** El servicio declara
  explícitamente "sólo logging".
- **Endurecer `traefik` en el compose.** Monta el socket de Docker y necesita
  capacidades; el hardening de este RC se limita al servicio `app` (D7).
- **Sustituir `deploy.replicas` por `--scale`/réplicas reales en compose.** Hoy
  el compose no orquesta réplicas sin Swarm; es un problema de despliegue
  preexistente, ajeno a los hallazgos del review.

## 11. Decisiones cerradas y preguntas abiertas

**Cerradas por el usuario (2026-11-03):**

1. **`CONSUL_SERVICE_TAGS` con comas:** no hay inventario de entornos, así que
   **no** se hace fail-fast ni se adivina compatibilidad. Se mitiga con el log
   de tags efectivos al arrancar + README + nota de release (T34, D2).
2. **Formato del ID:** se adopta `crypto/rand.Text()` (D6). Es único con
   probabilidad despreciable de colisión (≥ 128 bits, sin coordinación entre
   réplicas), y el contrato de validación no cambia. Coste aceptado: el README
   deja de prometer UUID y el tooling que auto-detecta UUIDs no lo verá.
3. **Hardening de compose:** **entra en este RC** (D7, T41).

**Abierta:**

1. ¿Quién ejecuta el gate Docker del Checkpoint K (requiere acceso a registry)?

## 12. Plan adicional — frontera de configuración

### Objetivo

Evitar lecturas directas de `os.Getenv` en `cmd/server/main.go`. `main` debe
actuar como composition root: recibir configuración ya resuelta y construir
clientes y servicios, sin defaults, trims ni validaciones.

### Decisiones

- Usar subestructuras anidadas para Consul/registro, logging e infraestructura
  PDF.
- `CONSUL_*` pertenece a `internal/config`.
- `LOG_LEVEL` y `LOG_FORMAT` permanecen en `internal/logging`, con configuración
  explícita.
- `PDF_OXIDE_LIB_PATH` permanece en infraestructura PDF, con inyección
  explícita.
- No agregar variables de entorno ni dependencias.

### Tareas

#### C1 — Configuración de Consul y registro

- [ ] Mover lectura, defaults, trims, validación y separación de tags a
  `internal/config`.
- [ ] Eliminar `os.Getenv` de `cmd/server/main.go`.
- [ ] Preservar Consul opcional, validación de campos obligatorios y comas
  internas en tags.
- [ ] Cubrir con tests de `internal/config` y `serviceRegistration`.

#### C2 — Logging e infraestructura explícitos

- [ ] Reemplazar lecturas implícitas de entorno en logging y PDF por
  configuración explícita.
- [ ] Conservar defaults, validaciones y el skip de integración sin librería
  nativa.
- [ ] Mantener tests de logging y extractor.

#### C3 — Verificación y documentación

- [ ] Ejecutar `gofmt -l .`, `go vet ./...`, `make test-unit` y `make build`.
- [ ] Verificar que `cmd/server/main.go` no tenga lecturas de entorno.
- [ ] Actualizar documentación solo si cambia el contrato visible.

### Checkpoint de configuración

- [ ] C1, C2 y C3 completadas.
- [ ] Comportamiento de Consul, logging y extracción preservado.
- [ ] Revisar el diff antes de continuar con T34–T41.
