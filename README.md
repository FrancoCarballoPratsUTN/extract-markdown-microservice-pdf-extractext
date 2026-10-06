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
| `MAX_WORKERS` | `2` | PDFium worker processes: the CPU parallelism |
| `QUEUE_SIZE` | `56` | Bounded queue depth in front of the workers |
| `QUEUE_MAX_MB` | `256` | Byte budget shared by running and queued jobs |
| `READ_TIMEOUT` / `WRITE_TIMEOUT` | `30` | Seconds |
| `PDFIUM_WORKER_PATH` | sibling of `server` | Worker binary location |

`PDFIUM_WORKER_PATH` defaults to `pdfium-worker` next to the running server, so
the shipped layout needs no override. It exists so a local build can point at a
binary outside `bin/`.

`MAX_WORKERS` is sized against the CPU quota, not against memory: each replica
has a hard `cpus: 1.0` limit, so workers past the second only split the same core
and lengthen latency without adding throughput. Measured, `2` beat `4` and `4`
beat `1`; see [Tuning the workers and the queue](#tuning-the-workers-and-the-queue).

Admission is `MAX_WORKERS + QUEUE_SIZE` in-flight requests per replica, because
that is exactly what the pool can be working on or waiting on. The middleware
rejects with `503` only when the pool itself has no room, and `QUEUE_MAX_MB`
bounds the bytes the queue may hold so a burst of large PDFs cannot OOM the
container.

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

Both need a running stack and their binaries on `PATH`
(`k6 v2.3.0`, `vegeta v12.13.0`). k6 resolves `open()` against the script's own
directory, so it reads the fixtures from `../../testdata` unless `CORPUS`
overrides that.

```bash
k6 run scripts/k6/spike.js

TARGET=https://extract.universidad.localhost ./scripts/vegeta/attack.sh
```

`TARGET`, `CORPUS`, `RATE`, `DURATION` and `TIMEOUT` are overridable. Both
scripts default to the enunciado profile: k6 ramps 10 s to 100 VUs, holds 20 s
and ramps down over 10 s; vegeta drives 50 req/s for 30 s with a 30 s timeout.
Both rotate the four PDFs of `testdata/`. The shared Traefik owns `80`/`443` and
redirects to HTTPS, and its certificate comes from mkcert, which is not in the
client trust store, so point `SSL_CERT_FILE` at the mkcert root or pass the
insecure flag of your tool.

### Measured capacity

The numbers below rotate the four PDFs of `testdata/`, which is what the
enunciado measures. The headline comparison is medians of 3 k6 runs on a 6 vCPU
host, against the shipped `MAX_WORKERS=2` / `QUEUE_SIZE=56` tuning:

| Métrica | Mínimas | Ours | |
| --- | ---: | ---: | --- |
| Throughput | 25.35 req/s | **31.08 req/s** | **+22.6%** |
| Errores | 0.00% | 0.02% (1 de 3 runs) | igual |
| p50 | 1.88 s | 2.43 s | 1.3x peor |
| p90 | 7.83 s | **4.43 s** | **1.8x mejor** |
| p95 | 8.80 s | **5.06 s** | **1.7x mejor** |
| Máximo | 13.94 s | **6.39 s** | **2.2x mejor** |

Per run: 31.08 / 31.06 / 31.15 req/s, with two runs at zero failures and one
losing a single request out of 1305. Throughput is tight across the three, but
read it as "~31 req/s": the host is a shared desktop and its load average was
between 4 and 15 during these runs, so treat any difference of a few percent
against another session as host noise rather than a change in the service. No
container restarted, nothing was OOM-killed, and the highest memory seen was
456.7 MiB against the 1024 M limit.

The p50 is the one metric we lose: their distribution is bimodal and ours is
flat, because we queue where they rejected. On vegeta (50 req/s, 30 s) the gap is
much wider, at **100.00% success against 66.53%**:

| Run | Requests | Success | p50 | p90 | p95 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 967 | 100.00% | 0.98 s | 4.40 s | 5.07 s | 7.62 s |
| 2 | 824 | 100.00% | 1.28 s | 4.43 s | 5.19 s | 8.34 s |

That is 27.5-32.2 req/s effective, every response `200` and no `503` at all,
against 16.65 req/s at 66.53% for the reference.

Throughput is capped by the host, not by the service: the five replicas hold a
hard `cpus: 1.0` quota each, so they can claim at most 5 of the 6 cores and
Traefik plus the load generator share what is left. This is also why the service
cannot absorb vegeta's 50 req/s without queueing.

### Where the time actually goes

Measured per phase over the 16-page Scrum guide, one extraction, same corpus the
load tests rotate:

| Phase | Over a worker process | In process |
| --- | ---: | ---: |
| `FPDFText_LoadPage` | 40.9 ms | 36.0 ms |
| `FPDFText_GetText` | 3.6 ms | 0.5 ms |
| `FPDFText_CountChars` | 2.8 ms | 0.0 ms |
| `FPDFText_ClosePage` | 2.3 ms | 0.1 ms |
| `OpenDocument` | 1.2 ms | 0.2 ms |
| `FPDF_CloseDocument` | 0.9 ms | 0.4 ms |
| **Total** | **51.5 ms** | **37.4 ms** |

Two things follow. `FPDFText_LoadPage` is ~80% of the request and it is real
PDFium parsing work, not transport, so no change to the extraction logic removes
it. The remaining ~14 ms (27%) is go-plugin round trips, 67 per request at
~0.21 ms each.

Collapsing the four per-page calls into one with `GetPageText` was tried and
**rejected on measurement**: it cuts the round trips from 67 to 19 but replaces
the one bulk `GetPageText` with a per-character read inside the worker, landing
at 49.2 ms against 49.1 ms for the current four-call sequence, a wash. Do not
revisit it without new evidence. The only lever left is a hand-written cgo
binding that batches the four calls into a single round trip while keeping the
bulk read, estimated at roughly 20%, at the cost of owning the memory-safety
handling of the hot path.

### The queue is what fixes the tail

With `MAX_WORKERS` as the only admission limit, the 21st simultaneous request
per replica is rejected with `503`, which is what produces a p90 of 7.83 s in the
reference. Queuing turns that rejection into latency:

| Concurrency | Throughput | 503 rate |
| --- | --- | --- |
| 10 | 26.55 req/s | 0.66% |
| 15 | 24.01 req/s | 3.50% |
| 20 | 24.54 req/s | 8.50% |

The queue removes every rejection *and* raises throughput, because a replica no
longer answers `503` while it still has an idle worker.

### Tuning the workers and the queue

Both knobs were swept with k6 on the enunciado profile, three interleaved passes
per configuration so that host drift hits every cell equally. Medians of 3:

| `MAX_WORKERS` | `QUEUE_SIZE` | In flight | Throughput | Errors | p50 | p90 | p95 | Max |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 4 | 32 | 36 | 27.84 req/s | 0.00% | 2.57 s | 4.77 s | 5.33 s | 6.87 s |
| 3 | 48 | 51 | 30.05 req/s | 0.00% | 2.46 s | 4.46 s | 5.34 s | 6.85 s |
| 2 | 32 | 34 | 28.99 req/s | 0.66% | 2.31 s | 4.85 s | 5.32 s | 7.15 s |
| 2 | 40 | 42 | 28.59 req/s | 0.00% | 2.48 s | 4.80 s | 5.88 s | 7.00 s |
| 2 | 48 | 50 | 29.19 req/s | 0.00% | 2.63 s | 4.22 s | 4.55 s | 7.09 s |
| **2** | **56** | **58** | **30.68 req/s** | **0.00%** | **2.45 s** | **4.44 s** | **4.82 s** | **6.31 s** |
| 2 | 64 | 66 | 28.61 req/s | 0.00% | 2.48 s | 4.50 s | 4.71 s | 6.83 s |

Two independent effects, both measured. Dropping `MAX_WORKERS` from 4 to 2 is
worth roughly +4% throughput, because past the second worker the replicas only
split their own `cpus: 1.0` quota: measured in isolation, `2` beat `4`, and `4`
beat `1`, so 2 is a genuine optimum rather than "less is more". Queue depth
trades p50 against rejections -- `QUEUE_SIZE=32` has the best p50 of the sweep but
the only nonzero error rate, because 34 in-flight slots per replica is not enough
headroom for Traefik's load imbalance. `56` is where errors hit zero without
paying for it in p50. `64` buys nothing further.

`QUEUE_SIZE=56` does not mean the profile's 100 VUs are buffered: 58 in flight
per replica is 290 across the five, far more than the profile ever offers. The
queue is headroom against the proxy's imbalance, and `QUEUE_MAX_MB=256` is what
bounds memory -- peak observed was 456.7 MiB per replica.
