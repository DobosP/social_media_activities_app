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
# collectstatic needs no database connection.
RUN DJANGO_SETTINGS_MODULE=config.settings.prod python manage.py collectstatic --noinput

EXPOSE 8000

# ASGI (daphne) so the REST API and real-time chat WebSockets (D5) are served from
# one process. Dev compose overrides this with `runserver`; on Render, $PORT is
# injected (see render.yaml).
CMD ["daphne", "-b", "0.0.0.0", "-p", "8000", "config.asgi:application"]
