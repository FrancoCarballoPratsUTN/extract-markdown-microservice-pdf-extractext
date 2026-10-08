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
