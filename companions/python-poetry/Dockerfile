# The builder image, used to build the virtual environment
FROM python:3.11-slim-buster as builder

RUN apt-get update

RUN apt-get install -y libpq5

RUN rm -rf /var/lib/apt/lists/*


RUN pip install --upgrade pip

RUN pip install poetry

ENV POETRY_NO_INTERACTION=1 \
    POETRY_NO_ROOT=1 \
    POETRY_VIRTUALENVS_IN_PROJECT=0 \
    POETRY_VIRTUALENVS_CREATE=1 \
    POETRY_CACHE_DIR=/app/.cache \
    POETRY_VIRTUALENVS_PATH=/venv

WORKDIR /app
