# Base images pinned by digest, not just tag. A published, signed
# aegis-api digest is a claim about exactly what bytes were built and run;
# a mutable tag (golang:1.25-alpine, distroless:nonroot) can point at a
# different image tomorrow without this Dockerfile changing, which would
# make the signature verify while the actual base silently drifted.
# Digests are the multi-platform index digest (docker buildx imagetools
# inspect <ref>), so both linux/amd64 and linux/arm64 resolve from the
# same pin. Human-readable tag kept alongside for readability; the digest
# is what's actually resolved.
FROM golang:1.25-alpine@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=dev
ARG COMMIT=unknown
# CGO_ENABLED=0 for a static binary — nothing in the runtime image can
# link against it. -trimpath keeps build-machine paths out of the binary.
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/nhatminh06/aegis-api/internal/api.Version=${VERSION} -X github.com/nhatminh06/aegis-api/internal/api.Commit=${COMMIT}" \
      -o /out/aegis-api ./cmd/server

# distroless:static — no shell, no package manager, no libc even, since the
# binary is statically linked. debug builds (:debug tag) exist upstream if
# a shell is ever genuinely needed for troubleshooting; runtime doesn't.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:1b7b9f0f0e0a1d2155f531db587cc48ec26aaf97ab64364225f5bf18a054e66a
COPY --from=build /out/aegis-api /aegis-api
# nonroot in this base image is uid 65532, already the image's default
# USER — set explicitly so it survives a base image change unnoticed.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/aegis-api"]
