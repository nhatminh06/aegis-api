# Multi-stage: the runtime image never sees the Go toolchain.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=dev
# CGO_ENABLED=0 for a static binary — nothing in the runtime image can
# link against it. -trimpath keeps build-machine paths out of the binary.
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/nhatminh06/aegis-api/internal/api.Version=${VERSION}" \
      -o /out/aegis-api ./cmd/server

# distroless:static — no shell, no package manager, no libc even, since the
# binary is statically linked. debug builds (:debug tag) exist upstream if
# a shell is ever genuinely needed for troubleshooting; runtime doesn't.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/aegis-api /aegis-api
# nonroot in this base image is uid 65532, already the image's default
# USER — set explicitly so it survives a base image change unnoticed.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/aegis-api"]
