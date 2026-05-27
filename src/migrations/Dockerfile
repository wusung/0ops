# syntax=docker/dockerfile:1.7
# 0ops migrations image — ADR-0009：multi-stage distroless + pinned goose binary
#
# build: podman build -f migrations/Dockerfile -t localhost/0ops-migrations:runtime .
# usage (compose): command: ["$DATABASE_URL", "up"]
# entrypoint:      goose -dir /migrations postgres <DATABASE_URL> <subcommand>

# --- builder：拉 pinned goose binary ---
FROM golang:1.26-alpine AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS="-trimpath"
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/pressly/goose/v3/cmd/goose@v3.22.1

# --- runtime：distroless static + goose + migrations/ ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=builder /go/bin/goose /usr/local/bin/goose
COPY migrations/ /migrations/
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/goose", "-dir", "/migrations", "postgres"]
