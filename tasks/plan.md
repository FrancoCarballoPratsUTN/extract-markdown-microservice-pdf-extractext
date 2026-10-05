# Implementation Plan: PDF to Markdown Microservice

## Overview

Build a high-concurrency PDF to Markdown extraction microservice in Go with 3-layer architecture (HTTP, Business, Infrastructure), implementing early backpressure, Docker multi-stage build, docker-compose with 5 replicas and Traefik, plus k6 and Vegeta test scripts.

## Architecture Decisions

- **Error responses**: Follow RFC 9457 (Problem Details for HTTP APIs) for consistent error formatting
- **Language**: Go - excellent for high concurrency, low memory, fast HTTP serving
- **PDF extraction**: Use Pdfium via CGO (e.g., github.com/bramp/pdfium or similar binding) for maximum native performance in text extraction as requested
- **HTTP framework**: `chi` or standard `net/http` - standard library is fine, chi for routing if needed; keep minimal
- **Architecture**: Strict 3-layer (presentation/http, business/usecase, infrastructure) with interfaces for testability
- **Backpressure**: Semaphore before reading body + configurable max upload size + worker pool with bounded queue
- **Config**: Environment variables (12-Factor), no disk writes for processing
- **Logging**: JSON to stdout

## Task List

### Phase 1: Foundation (Project Setup)
- [ ] **Task 1:** Initialize Go module and dependencies
- [ ] **Task 2:** Define project structure (3-layer layout)
- [ ] **Task 3:** Configuration via environment variables
- [ ] **Task 4:** Error types and domain models

### Checkpoint: Foundation
- [ ] Module builds with `go build ./...`
- [ ] No compilation errors

### Phase 2: Core Extraction Logic
- [ ] **Task 5:** PDF extraction service (infrastructure layer)
- [ ] **Task 6:** Business/usecase layer with extraction logic
- [ ] **Task 7:** Request/response DTOs and validation

### Checkpoint: Core Features
- [ ] Service can extract from a sample PDF
- [ ] Unit tests pass for extraction logic

### Phase 3: HTTP Server with Backpressure
- [ ] **Task 8:** HTTP handlers for POST /extract
- [ ] **Task 9:** Middleware for backpressure (semaphore + limits)
- [ ] **Task 10:** Server setup and graceful shutdown

### Checkpoint: HTTP Layer
- [ ] Endpoint accepts multipart/form-data and returns proper JSON
- [ ] Backpressure rejects when saturated (503/429)

### Phase 4: Containerization & Orchestration
- [ ] **Task 11:** Multi-stage Dockerfile
- [ ] **Task 12:** docker-compose.yml with 5 replicas, Traefik, resource limits
- [ ] **Task 13:** Traefik configuration/routing

### Checkpoint: Deployment
- [ ] Docker image builds successfully
- [ ] docker-compose config is valid

### Phase 5: Testing & Documentation
- [ ] **Task 14:** k6 spike test script
- [ ] **Task 15:** Vegeta load test script
- [ ] **Task 16:** Technical documentation (README + guide)

### Checkpoint: Complete
- [ ] All scripts present and documented
- [ ] Build and tests pass
- [ ] Ready for load testing

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| PDF extraction library choice (CGO vs pure Go) | High | Use pure Go (pdfcpu) to avoid CGO in Docker, easier multi-stage builds |
| Memory spikes with large PDFs | High | Early backpressure via semaphore before reading body + max size limits + process one request at a time per worker capacity |
| Timeouts in Vegeta/k6 | Medium | Tune worker pool, read timeouts, queue limits; backpressure rejects early |
| Traefik routing in compose | Medium | Use proper labels and network config |

## Open Questions
- Preferred PDF library? pdfcpu is solid for text/markdown; confirm license (Apache 2.0) acceptable
- Exact response schema - must include content (string) and page_count (int) as per spec
- Backpressure response code - spec says 503 or 429; choose 503 Service Unavailable for capacity with RFC 9457 problem details
- Max PDF size to enforce? Not specified; pick reasonable (e.g., 50MB) configurable via env
