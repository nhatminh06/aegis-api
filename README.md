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
runs as uid 65532. Both base images are pinned by digest, not a mutable
tag, so a signed release digest doesn't depend on a base image silently
moving underneath it.

## Releasing

Ordinary pushes to `main` (`.github/workflows/ci.yml`) only run
`go vet`/`go test`/`go build`. Nothing is published, scanned, or signed
until a version tag is pushed.

**Published semver tags are immutable.** A tag is never moved, re-cut, or
force-pushed once it exists — a new release always gets a new tag. If a
release needs correcting, that's a new version, not an edit to the old one.

Pushing a tag matching `v*` runs `.github/workflows/release.yml`:

```
verify the tagged commit is reachable from main
       |
tests / vet / build
       |
multi-platform build (linux/amd64, linux/arm64), pushed under a
staging identity (sha-<commit>) — not the release tag yet
       |
Trivy scan of the exact digest, both platforms, fails on HIGH/CRITICAL
       |
SBOM (SPDX JSON) generated per platform via Syft
       |
Cosign keyless signature on the exact digest (GitHub Actions OIDC,
no private key held anywhere) + SBOM attached as attestations
       |
signature verified in the same job, with issuer + identity constraints
       |
ONLY THEN: the same digest — never a rebuild — is promoted to the
release tag (ghcr.io/nhatminh06/aegis-api:vX.Y.Z)
```

The release tag and the scanned/signed digest are always the same bytes.
Nothing is rebuilt between the scan and the tag.

Deployment is controlled entirely by the Aegis repository. This repository
never touches Kubernetes directly — no deploy step, no kubeconfig, no
cluster credential anywhere in its CI. Once a release is signed, Flux
Image Automation running in the Aegis cluster discovers it, selects it,
and commits the exact digest into Aegis's own Git history; Kyverno's
admission policy there independently requires a valid signature from this
repository's release workflow before the image is allowed to run, so
automating *which* release Git records never automates *trusting* it.

## Graceful shutdown

On `SIGTERM`/`SIGINT`, the server marks itself not-ready immediately (so
Kubernetes/Gateway stop sending new traffic), waits a short grace period
for that to take effect, then shuts down the HTTP server with a timeout
rather than killing in-flight connections outright.
