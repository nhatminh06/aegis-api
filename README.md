# aegis-api

A small Go HTTP API used to exercise [Aegis](https://github.com/nhatminh06/aegis)'s
application-operating model — real routing, metrics, health/readiness
behavior, and policy — with actual traffic rather than a static demo
workload.

This repository owns the application: source, tests, container build, and
release publishing. It does not deploy itself. **Deployment is controlled
by the Aegis repository**, which pins an explicit image reference in Git
and reconciles it via Flux.

## Endpoints

| Method | Path            | Purpose                                          |
|--------|-----------------|---------------------------------------------------|
| GET    | `/healthz`      | Process liveness. Always 200 if the process is up. |
| GET    | `/readyz`       | Readiness to receive traffic. 503 during startup warm-up and shutdown draining. |
| GET    | `/metrics`      | Prometheus exposition format.                     |
| GET    | `/api/v1/info`  | Static JSON: name, version, environment.           |
| GET    | `/api/v1/work?value=N` | Computes `fib(N)` for `0 <= N <= 40`. Non-integer, negative, or out-of-range `N` returns `400` with a JSON error body. |

`/healthz` and `/readyz` are intentionally separate signals, not aliases:
the split is what lets a future real dependency affect routing without
also causing an unnecessary process restart, even though today the only
readiness input is startup/shutdown timing.

## Metrics

- `aegis_api_http_requests_total{method,path,status}` — counter
- `aegis_api_http_request_duration_seconds{method,path}` — histogram
- `aegis_api_work_requests_total` — counter, incremented only on accepted
  `/api/v1/work` requests

## Local development

```
go test ./...
go build ./cmd/server
PORT=8080 ./server
```

## Container build

```
docker build -t aegis-api:local --build-arg VERSION=dev .
docker run --rm -p 8080:8080 aegis-api:local
curl localhost:8080/healthz
```

The image is a multi-stage build: compiled with the Go toolchain, run from
`gcr.io/distroless/static-debian12:nonroot` — no shell, no package manager,
runs as uid 65532.

## Releasing

Push a tag matching `v*` (e.g. `v0.1.0`). CI runs the test suite, then
builds and pushes `ghcr.io/nhatminh06/aegis-api:<tag>` to GitHub Container
Registry. Untagged pushes to `main` only run tests — publishing a version
is a deliberate, explicit act.

## Graceful shutdown

On `SIGTERM`/`SIGINT`, the server marks itself not-ready immediately (so
Kubernetes/Gateway stop sending new traffic), waits a short grace period
for that to take effect, then shuts down the HTTP server with a timeout
rather than killing in-flight connections outright.
