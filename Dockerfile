# --- Frontend build stage: the React/Vite SPA (frontend/) compiles to hashed
# static assets + a manifest that collectstatic below bakes into the image.
# @roedu/ui comes from the committed tarball in frontend/vendor/ (no registry auth).
FROM node:22-slim AS frontend

WORKDIR /fe/frontend
COPY frontend/package.json frontend/package-lock.json ./
COPY frontend/vendor ./vendor
RUN npm ci
COPY frontend ./
RUN npm run build

FROM python:3.12-slim AS base

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

# GeoDjango native libraries (GDAL/GEOS/PROJ). This is the #1 GeoDjango gotcha.
RUN apt-get update && apt-get install -y --no-install-recommends \
        binutils \
        gdal-bin \
        libgdal-dev \
        libgeos-dev \
        libproj-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Built SPA assets from the frontend stage (vite outDir is ../static/frontend,
# i.e. /fe/static/frontend there) land where STATICFILES_DIRS expects them.
COPY --from=frontend /fe/static/frontend /app/static/frontend

# Bake static assets into the image so WhiteNoise can serve them at runtime.
# collectstatic needs no database connection. Prod settings now require DJANGO_SECRET_KEY
# (fail-closed; see config/settings/prod.py), so pass a throwaway value for this build-only
# step — it never reaches runtime, where a real key must be provided via the environment.
# IDENTITY_ALLOW_DEV_PROVIDER lets this static-only step past the prod fail-closed identity
# check (collectstatic needs no identity provider); the real provider is still required at
# runtime via the environment.
RUN DJANGO_SECRET_KEY=build-time-only-not-used-at-runtime \
    IDENTITY_ALLOW_DEV_PROVIDER=True \
    DJANGO_SETTINGS_MODULE=config.settings.prod python manage.py collectstatic --noinput

# P1 hardening: drop to an unprivileged user so a container escape / RCE doesn't land as root.
# Done AFTER collectstatic (which writes staticfiles/) and the apt/pip layers (which need root).
RUN useradd --system --create-home --uid 10001 appuser \
    && chown -R appuser:appuser /app
USER appuser

EXPOSE 8000

# ASGI (daphne) so the REST API and real-time chat WebSockets (D5) are served from
# one process. Dev compose overrides this with `runserver`; on Render, $PORT is
# injected (see render.yaml).
CMD ["daphne", "-b", "0.0.0.0", "-p", "8000", "config.asgi:application"]
