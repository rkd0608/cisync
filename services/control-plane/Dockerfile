# syntax=docker/dockerfile:1
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/control-plane ./cmd/control-plane

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --no-create-home sauron
WORKDIR /app
COPY --from=build /out/control-plane /app/control-plane
COPY migrations/ /app/migrations/
USER sauron
EXPOSE 8081
ENTRYPOINT ["/app/control-plane"]
