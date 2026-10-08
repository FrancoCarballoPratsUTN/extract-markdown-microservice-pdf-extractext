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
