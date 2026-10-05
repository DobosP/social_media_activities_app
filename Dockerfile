# syntax=docker/dockerfile:1
# Native release. Django sources remain offline contract oracles (Dockerfile.reference).
FROM node:24-bookworm-slim AS frontend
WORKDIR /build/frontend
COPY frontend/package.json frontend/package-lock.json ./
COPY frontend/vendor ./vendor
RUN npm ci
COPY frontend ./
RUN npm run build

FROM golang:1.27.1-bookworm AS backend
ARG GOMAXPROCS=2
ARG GOFLAGS=-p=2
WORKDIR /build/services/server
COPY services/authcore/ ../authcore/
COPY services/server/go.mod services/server/go.sum ./
RUN go mod download
COPY services/server/ ./
RUN GOMAXPROCS="$GOMAXPROCS" GOFLAGS="$GOFLAGS" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/social-server ./cmd/social-server

FROM debian:bookworm-slim AS runtime
# Codecs run under prlimit. No Python interpreter or framework is installed.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl libpcre2-8-0 ffmpeg libavif-bin util-linux perl-base \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 app \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin app
WORKDIR /app
COPY --from=backend /out/social-server /usr/local/bin/social-server
COPY services/authcore/LICENSE /usr/share/doc/social-server-authcore/LICENSE
COPY templates/ ./templates/
COPY apps/web/templates/ ./apps/web/templates/
COPY locale/ ./locale/
COPY static/ ./static/
COPY --from=frontend /build/static/frontend/ ./static/frontend/
COPY db/seed-data.sql ./db/seed-data.sql
RUN mkdir -p var/media var/media-work var/agent_snapshot \
    && chown -R 10001:10001 var && chmod 700 var var/media var/media-work var/agent_snapshot
ENV PORT=8000
USER 10001:10001
EXPOSE 8000
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD probe_host="${SITE_BASE_URL#*://}"; probe_host="${probe_host%%/*}"; curl --fail --silent --show-error --output /dev/null --max-time 3 --header "Host: ${probe_host:-127.0.0.1}" "http://127.0.0.1:${PORT:-8000}/healthz"
CMD ["sh", "-c", "exec social-server --listen \"0.0.0.0:${PORT:-8000}\" --site-root /app --static-dir /app/static --media-scratch /app/var/media-work"]

# Export with --target release --output type=local,dest=<task scratch>.
FROM scratch AS release
COPY --from=runtime /usr/local/bin/social-server /social-server
COPY --from=runtime /app/ /
COPY --from=runtime /usr/share/doc/social-server-authcore/ /licenses/authcore/
COPY deploy/systemd/ /deploy/systemd/
COPY deploy/backup.sh /deploy/backup.sh

FROM runtime AS production
