# Sauron ingest — dev/demo image. Build: docker build -t sauron/ingest services/ingest
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ingest ./cmd/ingest

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --no-create-home sauron
WORKDIR /app
COPY --from=build /out/ingest /app/ingest
COPY migrations/ /app/migrations/
USER sauron
EXPOSE 8080
ENTRYPOINT ["/app/ingest"]
