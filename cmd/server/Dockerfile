# syntax=docker/dockerfile:1.7
# 0ops-server — multi-stage：deps → builder → dev (air) / runtime (distroless nonroot)
# build runtime:  podman build --target runtime -f cmd/server/Dockerfile -t localhost/0ops-server:runtime .
# build dev:      automatically via compose (target=dev)

# --- deps：拉模組，最大化快取 ---
FROM golang:1.26-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# --- builder：編譯靜態 binary ---
FROM deps AS builder
ARG VERSION=dev
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w -X github.com/winshare/zeroops/internal/shared.Version=${VERSION}" \
    -o /out/0ops-server ./cmd/server

# --- dev：熱重載（air），bind-mount source via compose ---
FROM golang:1.26-alpine AS dev
RUN go install github.com/air-verse/air@v1.62.0
WORKDIR /app
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

# --- runtime：distroless 靜態 binary ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=builder /out/0ops-server /usr/local/bin/0ops-server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/0ops-server"]
