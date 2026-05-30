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

EXPOSE 8000

# ASGI (daphne) so the REST API and real-time chat WebSockets (D5) are served from
# one process. Dev compose overrides this with `runserver`; on Render, $PORT is
# injected (see render.yaml).
CMD ["daphne", "-b", "0.0.0.0", "-p", "8000", "config.asgi:application"]
