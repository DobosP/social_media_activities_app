# syntax=docker/dockerfile:1
FROM golang:1.27.3-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
COPY go.mod ./
COPY *.go ./
COPY landing.md openapi.json ./
RUN go build -trimpath -ldflags="-s -w" -o /agentapi .

FROM scratch
COPY --from=builder /agentapi /agentapi
USER 65534
EXPOSE 8090
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/agentapi", "--healthcheck"]
ENTRYPOINT ["/agentapi"]
