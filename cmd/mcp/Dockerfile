# syntax=docker/dockerfile:1.7
# 0ops-mcp — multi-stage：deps → builder → runtime (distroless nonroot)
# build: podman build --target runtime -f cmd/mcp/Dockerfile -t localhost/0ops-mcp:runtime .

FROM golang:1.25-alpine AS deps
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
    -o /out/0ops-mcp ./cmd/mcp

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=builder /out/0ops-mcp /usr/local/bin/0ops-mcp
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/0ops-mcp"]
