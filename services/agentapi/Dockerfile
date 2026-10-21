# syntax=docker/dockerfile:1
FROM golang:1.27.3-bookworm@sha256:21ade014faf952365b9ecbd8dbdb87176fbacb82fac9fb132d9f2a7b8cc3f132 AS builder
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
