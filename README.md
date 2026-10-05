# extract-markdown-microservice-pdf-extractext

High-concurrency PDF to Markdown extraction microservice in Go. Three-layer
architecture, bounded worker pool, RFC 9457 errors, 12-Factor.

## Endpoint

`POST /extract` accepts `multipart/form-data` (field `file`) or a raw
`application/pdf` / `application/octet-stream` body.

```json
{
  "content": "Ken Schwaber & Jeff Sutherland\n...",
  "page_count": 16
}
```

Errors are `application/problem+json` (RFC 9457):

```json
{ "type": "about:blank", "title": "Bad Request", "status": 400, "detail": "invalid pdf" }
```

| Status | Meaning |
| --- | --- |
| 400 | Empty body, corrupt PDF, or unsupported content type |
| 413 | Body exceeds `MAX_UPLOAD_MB` |
| 503 | Admission control saturated (too many in-flight requests) |
| 500 | PDFium worker failure |

## Quick start

The container carries PDFium, so no host setup is required:

```bash
docker compose up --build            # or: docker build -t pdf-extract-md .
docker run --rm -p 8080:8080 pdf-extract-md

curl -X POST --data-binary @testdata/2020-Scrum-Guide-Spanish-Latin-South-American.pdf \
  -H "Content-Type: application/pdf" http://localhost/8080/extract
```

## The PDFium dependency

PDFium is a C++ library and is **never linked into the server binary**. The
service ships two binaries:

- `/app/server` — HTTP runtime, talks to workers over go-plugin.
- `/app/pdfium-worker` — a PDFium process, spawned per worker by the pool.

That split is deliberate. A segfault inside PDFium would take down a
single-threaded CGO build of the server; here it kills only the worker, and the
pool discards it and spawns a replacement. It also lets the pool size itself to
the CPU and memory budget of the container.

The pool is created by `pdf.NewPool`, which validates the worker path and then
borrows an instance before returning. A missing or unusable worker binary
therefore aborts startup:

```
pdfium pool unavailable: pdfium worker binary not found: /app/does-not-exist
```

instead of leaving a server that answers `500` to every request.

### What the image installs

| Component | Version | Why |
| --- | --- | --- |
| Go | 1.26 | matches `go 1.26.0` in `go.mod` |
| PDFium | `chromium/8057` (linux-x64) | the build the `go-pdfium` v1.21.x test matrix runs |
| glibc | bookworm (2.36) | runtime base matches the toolchain |

PDFium is downloaded from
[bblanchon/pdfium-binaries](https://github.com/bblanchon/pdfium-binaries/releases)
in the `pdfium` stage, described to cgo through `/opt/pdfium/pdfium.pc`. Only
`libpdfium.so` is copied into the runtime image (181 MB total).

To change the PDFium version, pass a build arg and re-run the tests — the
integration suite is what guards the upgrade:

```bash
docker build --target test --build-arg PDFIUM_VERSION=7988 .
```

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | Listen port |
| `MAX_UPLOAD_MB` | `50` | Request body ceiling |
| `MAX_WORKERS` | `10` | PDFium worker processes, and the admission limit |
| `QUEUE_SIZE` | `100` | Bounded queue depth |
| `READ_TIMEOUT` / `WRITE_TIMEOUT` | `30` | Seconds |
| `PDFIUM_WORKER_PATH` | sibling of `server` | Worker binary location |

`PDFIUM_WORKER_PATH` defaults to `pdfium-worker` next to the running server, so
the shipped layout needs no override. It exists so a local build can point at a
binary outside `bin/`.

`MAX_WORKERS` drives memory: each idle worker costs roughly 28 MB resident, so
size it against the container's memory limit and CPU quota. On 1 CPU it should
stay near `4`; see [Tuning `MAX_WORKERS`](#tuning-max_workers).

## Layout

```
cmd/server          composition root: config, pool, http, graceful shutdown
cmd/pdfium-worker   PDFium plugin process (spawned by the pool, not run by hand)
internal/domain     entities + error taxonomy (no dependencies)
internal/usecase    business rules; owns the Extractor port it depends on
internal/infrastructure/pdf   PDFium pool + text-extraction adapter
internal/presentation/http    handlers, payload parsing, backpressure
```

Dependencies point inward. `usecase` declares the `Extractor` interface it needs;
`infrastructure/pdf` implements it. The business layer never imports an adapter,
so swapping PDFium for another engine is a wiring change in `cmd/server`.

## Tests

```bash
docker build --target test .        # go vet, unit tests, and PDFium integration tests
```

The `test` stage runs the real suite against a real PDFium, including
`internal/infrastructure/pdf/extractor_integration_test.go`, which extracts the
PDFs in `testdata/` through a live worker pool.

Local runs need PDFium and a built worker:

```bash
export PKG_CONFIG_PATH=/opt/pdfium LD_LIBRARY_PATH=/opt/pdfium/lib
go build -o bin/pdfium-worker ./cmd/pdfium-worker
PDFIUM_WORKER_PATH=$PWD/bin/pdfium-worker go test -tags integration ./...
```

## Load testing

Both need a running stack. The script is mounted at `/scripts/spike.js` and
reads its fixture from `../../testdata`, which resolves to the mount root.

```bash
docker run --rm --network host \
  --add-host extract.universidad.localhost:127.0.0.1 \
  -v "$PWD/scripts/k6:/scripts" -v "$PWD/testdata:/testdata" \
  grafana/k6 run /scripts/spike.js

scripts/vegeta/attack.sh 50 30s https://extract.universidad.localhost/extract
```

`BASE_URL`, `PEAK_VUS` and `INSECURE_TLS` are overridable via `-e`. The shared
Traefik owns `80`/`443` and redirects to HTTPS, and its certificate comes from
mkcert, which is not in the k6 trust store, so `INSECURE_TLS` defaults to `true`.

### Measured capacity

One replica on 1 CPU extracts the 16-page fixture at roughly 13 req/s. Past
about 4 concurrent requests a replica is saturated: throughput stops improving
while latency keeps degrading.

| Replicas | Concurrency | Throughput | p95 | Errors |
| --- | --- | --- | --- | --- |
| 1 | 4 | 12.93 req/s | 0.368 s | 0% |
| 1 | 8 | 13.19 req/s | 0.682 s | 0% |
| 1 | 10 | 12.95 req/s | 0.871 s | 0% |
| 5 | 10 | 60.04 req/s | 0.195 s | 0% |
| 5 | 20 | 59.76 req/s | 0.330 s | 0.33% |
| 5 | 30 | 63.54 req/s | 0.396 s | 9.33% |

`scripts/k6/spike.js` at its default 15 VUs: **46.06 req/s, 0.00% errors,
p(95) 97 ms, 100% of checks passing** against the required
`>25.35 req/s` with `0.00%` errors.

The 5-replica ceiling is roughly 60 req/s. Past that the service is overloaded,
not broken: admission control returns `503` for the excess rather than queueing,
so error rate tracks load above capacity. A 0% error run requires an arrival
rate below capacity, which is why the script peaks at 15 VUs instead of
overdriving the service.

### Tuning `MAX_WORKERS`

`MAX_WORKERS` is both the worker count and the per-replica admission limit, and
it must be sized against the CPU limit: on a 1 CPU replica, `4` beat `10` on
every axis measured.

| `MAX_WORKERS` | Throughput (conc 10) | p95 | Resident memory |
| --- | --- | --- | --- |
| `10` | 47.49 req/s | 0.228 s | 236 MiB |
| `4` | 60.04 req/s | 0.195 s | 90 MiB |

`docker-compose.yml` therefore pins `MAX_WORKERS=4` for its `cpus: 1.0` limit.
Raising the limit would need a larger total admission ceiling than
`replicas x MAX_WORKERS`, and a bounded queue in front of the workers.