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

EXPOSE 8000

# Dev compose overrides this with `runserver`.
CMD ["gunicorn", "config.wsgi:application", "--bind", "0.0.0.0:8000"]
