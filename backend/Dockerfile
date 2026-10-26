# syntax=docker/dockerfile:1

# The API, its migrations, the demo seed and a healthcheck probe in one image.
# Compose runs /app/migrate once, then /app/api (see ../docker-compose.yml);
# `docker compose run --rm --entrypoint /app/seed api` loads the demo data.

# --- build: static binaries (no cgo, so no libc to depend on) ---
FROM golang:1.26.10-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    for cmd in api migrate seed healthcheck; do \
      go build -ldflags="-s -w" -o /out/$cmd ./cmd/$cmd || exit 1; \
    done

# --- runtime: distroless, no shell or package manager, non-root user ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ ./
COPY migrations ./migrations
# 65532:65532, "nonroot" in distroless.
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=4s --start-period=5s --retries=3 CMD ["/app/healthcheck", "http://localhost:8080/ready"]
ENTRYPOINT ["/app/api"]
