# syntax=docker/dockerfile:1.7
# mock-idp — dev/e2e-only minimal OIDC provider. Built by the e2e compose
# overlay (compose.e2e.yaml); NEVER part of production images.
# build context is ./src (matches the server/cli/mcp Dockerfiles).

FROM golang:1.26-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

FROM deps AS builder
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -o /out/mock-idp ./cmd/devtools/mock-idp

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=builder /out/mock-idp /usr/local/bin/mock-idp
USER nonroot:nonroot
EXPOSE 9000
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
    CMD ["/usr/local/bin/mock-idp", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/mock-idp"]
