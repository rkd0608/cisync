# Load generator runs INSIDE the compose network: macOS host port-forwarding
# collapses under per-port concurrency and would measure the proxy, not Sauron.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /out/loadgen .

FROM debian:bookworm-slim
COPY --from=build /out/loadgen /usr/local/bin/loadgen
ENTRYPOINT ["loadgen"]
