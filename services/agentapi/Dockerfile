# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS builder
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
ENTRYPOINT ["/agentapi"]
