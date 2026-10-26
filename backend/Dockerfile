# syntax=docker/dockerfile:1

# The API and its migrations in one image: `migrate` runs once, then `api`
# (see the "app" profile in ../docker-compose.yml).

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.22
RUN adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=build /out/api /out/migrate ./
COPY migrations ./migrations
USER app
EXPOSE 8080
ENTRYPOINT ["/app/api"]
