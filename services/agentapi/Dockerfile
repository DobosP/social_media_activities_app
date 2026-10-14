# syntax=docker/dockerfile:1
FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS builder
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
