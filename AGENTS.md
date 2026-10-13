# AGENTS.md

## Commands

| Task | Command |
|------|---------|
| Install the native pdf_oxide library | `make install` (or `go run github.com/yfedoseev/pdf_oxide/go/cmd/install@v0.3.78 -shared`) |
| Vet + unit tests | `make test` (`go vet ./... && go test ./...`) |
| Unit-tag tests (`//go:build unit`) | `make test-unit` (`go test -tags unit ./...`) |
| Integration tests | `make test-integration` (requires `PDF_OXIDE_LIB_PATH`) |
| Build the server | `make build` |
| Run the server locally | `make run` |
| Docker gate | `docker build --target test .` |

## Environment

- `CGO_ENABLED=0`: the build is pure Go. The pdf_oxide engine is loaded at run
  time via purego's `dlopen`.
- `PDF_OXIDE_LIB_PATH`: absolute path to `libpdf_oxide.so`. The server and the
  integration tests fail fast (boot probe / skip) without it.
- `MAX_PAGES`: per-document page cap enforced by the extractor (default 1000;
  `0` disables the limit). Exceeding it yields HTTP 413.
- `MAX_UPLOAD_MB`, `MAX_WORKERS`, `QUEUE_SIZE`, `QUEUE_MAX_MB`, `READ_TIMEOUT`,
  `WRITE_TIMEOUT`, `SHUTDOWN_TIMEOUT`, `PORT`: server configuration
  (see `internal/config`). Invalid values abort startup (fail-fast).
- `LOG_LEVEL` (`debug|info|warn|error`, default `info`) and `LOG_FORMAT`
  (`json|text`, default `json`): structured logging configuration, read by
  `internal/logging` (not `internal/config`). Invalid values abort startup.

## Conventions

- TDD (RED → GREEN → REFACTOR); tests live next to production code.
- Integration tests in `internal/infrastructure/pdf/` skip when
  `PDF_OXIDE_LIB_PATH` is unset.
- PDF fixtures live in `tests/stress/pdfs/`.
- Do not edit `tasks/plan.md` or `tasks/todo.md`; the pdf_oxide migration plan
  is tracked in `tasks/plan-markdown.md` and `tasks/todo-markdown.md`.