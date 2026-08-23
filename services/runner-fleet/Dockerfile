# Sauron runner-fleet — dev/demo image. Build: docker build -t sauron/runner-fleet services/runner-fleet
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
 && useradd --system --uid 10002 --no-create-home sauron
COPY --from=build /out/runner-fleet /usr/local/bin/runner-fleet
USER sauron
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/runner-fleet"]
