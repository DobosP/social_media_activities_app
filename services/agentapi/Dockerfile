# syntax=docker/dockerfile:1
FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS builder
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
