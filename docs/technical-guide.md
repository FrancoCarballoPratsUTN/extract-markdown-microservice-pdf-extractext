# Technical Guide - PDF to Markdown Microservice

## Architecture

3-layer architecture (Clean Architecture/SOLID):
- **Presentation/HTTP** (internal/presentation/http): Request handling, payload validation, RFC 9457 problem details, admission control (backpressure)
- **Business/Usecase** (internal/usecase): Orchestration, validation, worker pool for CPU-bound work
- **Infrastructure** (internal/infrastructure/pdf): Pdfium (CGO) for text extraction, pure in-memory

## Key Design Decisions

- **Pdfium via CGO**: Max native performance. Requires C deps in Docker (libpdfium-dev).
- **Backpressure**: Semaphore-based admission control - rejects with 503 early when saturated.
- **Worker pool**: Separates HTTP from CPU-bound extraction (cátedra req 4). Bounded queue rejects on full.
- **RFC 9457**: All errors use application/problem+json.
- **Stateless/12-Factor**: Env config, logs to stdout, no disk writes.

## Performance Analysis

Addresses: CPU-bound extraction (worker pool), memory (early rejection, size limits), queue saturation (bounded queue + semaphore).

## Deployment

```bash
docker-compose up --build
```

Scale: `docker-compose up --scale app=5`

## Testing

Unit: `go test -tags unit ./...`
Integration: `go test -tags integration ./...` (needs libpdfium)
