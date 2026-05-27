# syntax=docker/dockerfile:1.7
# 0ops CLI — multi-stage：deps → builder → runtime (distroless nonroot)
# build: podman build --target runtime -f cmd/cli/Dockerfile -t localhost/0ops-cli:runtime .

FROM golang:1.26-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

FROM deps AS builder
ARG VERSION=dev
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w -X github.com/winshare/zeroops/internal/shared.Version=${VERSION}" \
    -o /out/0ops ./cmd/cli

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=builder /out/0ops /usr/local/bin/0ops
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/0ops"]
