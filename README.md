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
| `EXTRACTOR_ENGINE` | `pdfium` | Extraction engine: `pdfium` or `mupdf` (see below) |
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
internal/infrastructure/mupdf MuPDF adapter (measured, not the default engine)
internal/benchmark      engine-level harness: per-document and rotating corpora
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
enunciado measures. Medians of 3 runs on a 6 vCPU host; the full methodology,
the engine comparison and the caveats are in
[`docs/extractor-benchmark.md`](docs/extractor-benchmark.md).

One replica on 1 CPU extracts 8.4 req/s of the rotating corpus, and the HTTP
layer costs about 1% on top of the engine.

| Replicas | Concurrency | Throughput | p90 | Errors |
| --- | --- | --- | --- | --- |
| 5 | 10 | 27.29 req/s | 0.512 s | 0% |
| 5 | 15 | 28.37 req/s | 0.712 s | 0% |
| 5 | 20 | 28.47 req/s | 1.066 s | 0% |
| 5 | 100 | 29.56 req/s | 0.982 s | 0% |

Those four come from the curl harness at low concurrency. The number that counts
is k6 on the exact enunciado profile, run against the shipped
`MAX_WORKERS=2` / `QUEUE_SIZE=56` tuning:

| Metric | Professor | Ours | |
| --- | ---: | ---: | --- |
| Throughput | 25.35 req/s | **29.15 req/s** | **+15.0%** |
| Errors | 0.00% | **0.00%** (0 of 6 runs) | equal |
| p50 | 1.88 s | 2.50 s | 1.3x worse |
| p90 | 7.83 s | **4.62 s** | **1.7x better** |
| p95 | 8.80 s | **4.97 s** | **1.8x better** |
| Max | 13.94 s | **6.18 s** | **2.3x better** |

Median of 6 runs. Throughput ranged 28.00-30.99 req/s between blocks as host load
moved, so read it as "~29 req/s" rather than the best case. Errors were zero in
every run, no container restarted, and the peak was 567 MiB per replica against
the 1024 M limit.

The p50 is the one metric we lose: their distribution is bimodal and ours is
flat, because we queue where they rejected. On vegeta (50 req/s, 30 s) the gap is
much wider: **23.80-24.43 req/s effective at 100.00% success, against 16.65
req/s at 66.53%**, with no `503` at all and a p50 of 0.92 s against 14.89 s.

Throughput is capped by the host, not by the service: the five replicas hold a
hard `cpus: 1.0` quota each, so they can claim at most 5 of the 6 cores and
Traefik plus the load generator share what is left. A single replica sustains
8.4 req/s of the rotating corpus, which extrapolates to ~42 req/s of extraction
capacity across five if the host had the cores to give. On this machine it does
not, which is also why the service cannot absorb vegeta's 50 req/s.

### `EXTRACTOR_ENGINE`

`pdfium` is the default and the only engine that is safe under concurrency.
`mupdf` is wired and measured, but `go-fitz`'s `Document.Text()` is **not safe for
concurrent use**: it runs the page content stream through cgo and segfaults the
process, which takes down all five replicas and makes Traefik answer `502`. Keep
it only for reproducing that behaviour:

```bash
EXTRACTOR_ENGINE=mupdf docker compose up -d
```

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
bounds memory -- peak observed was 567 MiB per replica.
