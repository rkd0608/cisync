# Sauron github-connector — dev/demo image. Build: docker build -t sauron/github-connector services/github-connector
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/github-connector ./cmd/github-connector

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --no-create-home sauron
WORKDIR /app
COPY --from=build /out/github-connector /app/github-connector
COPY migrations/ /app/migrations/
USER sauron
EXPOSE 8083
ENTRYPOINT ["/app/github-connector"]
