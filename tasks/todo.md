# Task List: PDF to Markdown Microservice

## Phase 1: Foundation

### Task 1: Initialize Go module and dependencies
**Description:** Initialize go.mod with module path and add required dependencies (pdfcpu for PDF extraction, chi optional, env parsing).
**Acceptance criteria:**
- [x] go.mod exists with correct module path
- [x] go.sum created
- [x] Dependencies added: github.com/klippa-app/go-pdfium
- [x] `go build ./...` succeeds with no errors

**Verification:**
- [x] `go mod tidy` runs cleanly
- [ ] `go build ./...` succeeds

**Dependencies:** None
**Files likely touched:** go.mod, go.sum
**Estimated scope:** XS (1 file)

### Task 2: Define project structure (3-layer layout)
**Description:** Create directory structure following 3-layer architecture: presentation/http, business/usecase, infrastructure/extractor, config, domain/models.
**Acceptance criteria:**
- [x] Directories created: `internal/config`, `internal/domain`, `internal/usecase`, `internal/infrastructure/pdf`, `internal/presentation/http`, `pkg/` if needed
- [x] Each layer has appropriate separation (interfaces defined where needed)
- [x] No circular dependencies

**Verification:**
- [ ] Directory tree matches planned layout
- [x] `go build ./...` still succeeds

**Dependencies:** Task 1
**Files likely touched:** Directory structure only
**Estimated scope:** XS

### Task 3: Configuration via environment variables
**Description:** Implement config struct reading from env vars per 12-Factor App (PORT, MAX_UPLOAD_MB, MAX_WORKERS, QUEUE_SIZE, READ_TIMEOUT, WRITE_TIMEOUT).
**Acceptance criteria:**
- [x] `internal/config/config.go` loads all required env vars with sensible defaults
- [x] Values validated (non-negative, reasonable bounds)
- [x] Configuration documented in code

**Verification:**
- [ ] Unit test for config loading (defaults + overrides)
- [x] `go build ./...` succeeds

**Dependencies:** Task 2
**Files likely touched:** internal/config/config.go, internal/config/config_test.go
**Estimated scope:** S (1-2 files)

### Task 4: Error types and domain models
**Description:** Define domain models and error types - ExtractRequest, ExtractResponse (content, page_count), domain errors.
**Acceptance criteria:**
- [x] `internal/domain/extract.go` defines ExtractResponse struct with `Content string` and `PageCount int` json tags
- [x] Domain errors defined (ErrInvalidPDF, ErrTooLarge, ErrServiceUnavailable, ErrExtractionFailed)
- [x] Response matches spec: application/json with content and page_count

**Verification:**
- [x] Struct tags correct for JSON marshaling
- [x] `go build ./...` succeeds

**Dependencies:** Task 2
**Files likely touched:** internal/domain/extract.go
**Estimated scope:** XS

### Task 4b: RFC 9457 Problem Details types
**Description:** Implement RFC 9457 Problem Details error types for consistent API error responses.
**Acceptance criteria:**
- [x] `internal/domain/problem.go` defines ProblemDetails struct per RFC 9457 (type, title, status, detail, instance)
- [x] Helper functions to create problem responses
- [x] Proper Content-Type: application/problem+json
- [x] Integration with existing error types

**Verification:**
- [x] Struct marshals correctly
- [x] `go build ./...` succeeds

**Dependencies:** Task 4
**Files likely touched:** internal/domain/problem.go
**Estimated scope:** XS

## Checkpoint: Foundation
- [x] All tasks 1-4 complete
- [x] `go build ./...` passes
- [x] `go vet ./...` passes

## Phase 2: Core Extraction Logic

### Task 5: PDF extraction service (infrastructure layer)
**Description:** Implement PDF text extraction using Pdfium via CGO. Extract text from PDF bytes and return content + page count. No disk writes.
**Acceptance criteria:**
- [x] `internal/infrastructure/pdf/extractor.go` implements extraction interface
- [x] Extracts text content from PDF (markdown-friendly; preserve basic structure)
- [x] Returns page count accurately
- [x] Handles errors (corrupt PDF, encrypted if applicable) and wraps as domain errors
- [x] Pure in-memory processing (no temp files)

**Verification:**
- [ ] Unit test with a minimal test PDF
- [ ] Handles invalid PDF gracefully
- [ ] `go build ./...` and tests pass

**Dependencies:** Task 4
**Files likely touched:** internal/infrastructure/pdf/extractor.go, internal/infrastructure/pdf/extractor_test.go
**Estimated scope:** M (2-3 files)

### Task 6: Business/usecase layer with extraction logic
**Description:** Implement usecase that orchestrates extraction with backpressure awareness and validation.
**Acceptance criteria:**
- [x] `internal/usecase/extract.go` defines Usecase interface and implementation
- [x] Validates input (nil/empty, size limits)
- [x] Calls infrastructure extractor
- [x] Returns domain models, proper error mapping
- [x] Stateless (12-Factor)

**Verification:**
- [x] Unit tests with mocks for extractor
- [x] Tests cover error cases
- [ ] `go test ./...` passes

**Dependencies:** Task 5
**Files likely touched:** internal/usecase/extract.go, internal/usecase/extract_test.go
**Estimated scope:** M (2-3 files)

### Task 7: Request/response DTOs and validation
**Description:** Define request payload reading (multipart + raw binary) with validation, and confirm the API contract matches the spec exactly (content, page_count). Response DTO reuses domain.ExtractResponse to avoid duplication.
**Acceptance criteria:**
- [x] Validation for multipart form (must contain PDF file on "file" field)
- [x] Content-Type handling correct (multipart/form-data, application/pdf, application/octet-stream)
- [x] Response contract reuses domain.ExtractResponse (no duplicate DTO)
- [x] Payload size limit enforced before extraction

**Verification:**
- [x] JSON tags verified (`content`, `page_count` in internal/domain/extract.go:12)
- [x] Tests pass: `go test ./internal/presentation/...`
- [x] Build succeeds: `go build ./...`
- [x] Manual check: covered by 9 table tests in payload_test.go

**Dependencies:** Task 6
**Files likely touched:** internal/presentation/http/payload.go, internal/presentation/http/payload_test.go
**Estimated scope:** S (2 files)

## Checkpoint: Core Features
- [x] Tasks 5-7 complete
- [ ] Extraction works end-to-end with sample PDF
- [ ] Unit tests pass

## Phase 3: HTTP Server with Backpressure

### Task 8: HTTP handlers for POST /extract
**Description:** Implement handler for POST /extract accepting multipart/form-data or binary body as per spec. Return 200 OK with JSON. Use RFC 9457 for error responses.
**Acceptance criteria:**
- [ ] Handler reads PDF from multipart form field (e.g., "file") or from request body if content-type is application/pdf
- [ ] Enforces max upload size
- [ ] Calls usecase and returns JSON with status 200 on success
- [ ] Proper error responses using RFC 9457 Problem Details (400 for bad request, 413 if too large, 500 for extraction errors, 503/429 for saturated)
- [ ] Logs to stdout (JSON or structured)

**Verification:**
- [ ] Manual test with curl against sample PDF
- [ ] Returns correct JSON structure
- [ ] `go test ./...` passes

**Dependencies:** Task 7
**Files likely touched:** internal/presentation/http/handler.go, internal/presentation/http/handler_test.go
**Estimated scope:** M (2 files)

### Task 9: Middleware for backpressure (semaphore + limits)
**Description:** Implement early backpressure - semaphore before reading body to reject when saturated. Configure worker limits.
**Acceptance criteria:**
- [ ] Semaphore-based admission control (max concurrent workers from config)
- [ ] Reject with 503 Service Unavailable when saturated (as per spec guidance)
- [ ] Max body size limit enforced
- [ ] Queue size/bounded behavior to prevent memory overflow
- [ ] Early rejection before full body read where possible

**Verification:**
- [ ] Behavior tested under simulated saturation
- [ ] Returns 503 when at capacity
- [ ] No memory leaks under load

**Dependencies:** Task 3, Task 8
**Files likely touched:** internal/presentation/http/middleware.go, internal/presentation/http/middleware_test.go
**Estimated scope:** M (2 files)

### Task 9b: Worker pool for extraction processing
**Description:** Implement bounded worker pool to separate HTTP runtime from extraction work (cátedra requirement 4). Prevents blocking HTTP handlers during CPU-intensive PDF extraction.
**Acceptance criteria:**
- [ ] Worker pool with configurable max workers and queue size
- [ ] Jobs submitted to pool; results returned via channels with timeout
- [ ] Queue full -> return 503 with proper backpressure
- [ ] Clean shutdown of workers
- [ ] Non-blocking admission control integrated with semaphore

**Verification:**
- [ ] Pool processes jobs correctly
- [ ] Queue bounds respected (reject when full)
- [ ] Graceful shutdown works
- [ ] `go test ./...` passes

**Dependencies:** Task 6, Task 9
**Files likely touched:** internal/usecase/workerpool.go, internal/usecase/workerpool_test.go
**Estimated scope:** M (2 files)

### Task 10: Server setup and graceful shutdown
**Description:** Wire up server with routing, timeouts, graceful shutdown, config injection.
**Acceptance criteria:**
- [ ] `cmd/server/main.go` initializes all layers with DI
- [ ] Router (chi or stdlib) mounts POST /extract
- [ ] Read/write timeouts from config
- [ ] Graceful shutdown on SIGTERM/SIGINT
- [ ] Listens on PORT env var (default 8080)
- [ ] Stateless, 12-Factor compliant

**Verification:**
- [ ] Server starts successfully
- [ ] Graceful shutdown tested
- [x] `go build ./...` passes

**Dependencies:** Task 9
**Files likely touched:** cmd/server/main.go
**Estimated scope:** S (1 file)

## Checkpoint: HTTP Layer
- [ ] Tasks 8-10 complete
- [ ] End-to-end test: curl POST /extract with PDF returns 200 + JSON
- [ ] Backpressure returns 503 when saturated
- [ ] All tests pass

## Phase 4: Containerization & Orchestration

### Task 11: Multi-stage Dockerfile
**Description:** Create optimized multi-stage Dockerfile for Go binary (small final image).
**Acceptance criteria:**
- [ ] Multi-stage: build stage with C/C++ toolchain for CGO/Pdfium, runtime stage with minimal libs for Pdfium
- [ ] CGO enabled for Pdfium; install required C/C++ build dependencies (pkg-config, cmake, libpdfium-dev or build Pdfium from source as needed)
- [ ] Minimal final image size
- [ ] Runs as non-root user
- [ ] Healthcheck if appropriate

**Verification:**
- [ ] `docker build -t pdf-extract-md:latest .` succeeds
- [ ] Container runs and responds to requests

**Dependencies:** Task 10
**Files likely touched:** Dockerfile
**Estimated scope:** S (1 file)

### Task 12: docker-compose.yml with 5 replicas, Traefik, resource limits
**Description:** Create docker-compose with up to 5 replicas, strict CPU/RAM limits (1.0 CPU, 512MB-1GB RAM per replica), Traefik load balancer.
**Acceptance criteria:**
- [ ] Service configured with `deploy.replicas: 5` (or scale)
- [ ] Resource limits: `cpus: '1.0'`, `memory: 512M` or up to 1G as specified
- [ ] Traefik reverse proxy configured with proper labels
- [ ] Exposes service on port 80/8080 via Traefik
- [ ] Shared network for Traefik and app
- [ ] Restart policy defined

**Verification:**
- [ ] `docker-compose config` validates
- [ ] `docker-compose up --scale app=5` starts (structure valid)

**Dependencies:** Task 11
**Files likely touched:** docker-compose.yml
**Estimated scope:** S (1 file)

### Task 13: Traefik configuration/routing
**Description:** Ensure Traefik routing works for /extract endpoint with load balancing across replicas.
**Acceptance criteria:**
- [ ] Traefik service defined in compose
- [ ] Labels on app service for routing (traefik.http.routers, services)
- [ ] Entry point configured (web on 80)
- [ ] Load balancing across replicas functional

**Verification:**
- [ ] Routing tested conceptually via compose config
- [ ] Traefik dashboard accessible if configured (optional)

**Dependencies:** Task 12
**Files likely touched:** docker-compose.yml (may extend), traefik.yml optional
**Estimated scope:** XS

## Checkpoint: Deployment
- [ ] Tasks 11-13 complete
- [ ] Docker build succeeds
- [ ] docker-compose.yml valid

## Phase 5: Testing & Documentation

### Task 14: k6 spike test script
**Description:** Create k6 spike test script to exceed 25.35 req/s with 0.00% errors as per requirements.
**Acceptance criteria:**
- [ ] `scripts/k6/spike.js` (or k6/spike.js) with spike scenario
- [ ] Targets POST /extract endpoint with test PDF
- [ ] Configured to validate 0% errors, measure throughput
- [ ] Includes proper setup and thresholds

**Verification:**
- [ ] Script syntax valid
- [ ] Documented how to run

**Dependencies:** Task 10
**Files likely touched:** scripts/k6/spike.js
**Estimated scope:** S (1 file)

### Task 15: Vegeta load test script
**Description:** Create Vegeta fixed load test script to improve on 16.65 req/s, 66.53% success from reference.
**Acceptance criteria:**
- [ ] `scripts/vegeta/attack.sh` or config with target rate, duration
- [ ] Sends multipart/form-data with test PDF to /extract
- [ ] Includes vegeta command with appropriate flags
- [ ] Results reporting (report, plot optional)

**Verification:**
- [ ] Script is executable and documented
- [ ] Targets correct endpoint format

**Dependencies:** Task 10
**Files likely touched:** scripts/vegeta/attack.sh, scripts/vegeta/targets.txt (or inline)
**Estimated scope:** S (1 file)

### Task 16: Technical documentation (README + guide)
**Description:** Create comprehensive technical guide with architecture justification, bottleneck analysis, deployment commands.
**Acceptance criteria:**
- [ ] Update README.md with overview, API spec, build/run instructions
- [ ] Create `docs/architecture.md` or technical guide covering 3-layer, SOLID/KISS/YAGNI, backpressure rationale
- [ ] Bottleneck analysis (queue saturation, memory, I/O)
- [ ] Deployment commands (docker-compose up, scaling)
- [ ] k6/Vegeta usage instructions

**Verification:**
- [ ] Documentation complete and accurate
- [ ] Commands tested

**Dependencies:** Tasks 11-15
**Files likely touched:** README.md, docs/technical-guide.md (or ARCHITECTURE.md)
**Estimated scope:** S (1-2 files)

## Checkpoint: Complete
- [ ] All tasks 1-16 complete
- [x] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] Docker builds successfully
- [ ] All scripts present
- [ ] Documentation complete

## Dependencies Graph
Foundation (1-4) → Core (5-7) → HTTP+Backpressure (8-10) → Containerization (11-13) → Testing/Docs (14-16)
