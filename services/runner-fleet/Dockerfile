# CISync runner-fleet — dev/demo image. Build: docker build -t cisync/runner-fleet services/runner-fleet
# The docker provider additionally needs a docker CLI + socket mount; sim (default) needs none.
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/runner-fleet ./cmd/runner-fleet

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10002 --no-create-home cisync
WORKDIR /app
COPY --from=build /out/runner-fleet /app/runner-fleet
COPY migrations/ /app/migrations/
USER cisync
EXPOSE 8082
ENTRYPOINT ["/app/runner-fleet"]
