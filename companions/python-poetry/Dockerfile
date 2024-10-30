FROM python:3.13-alpine as builder

RUN apk update && \
    apk add --no-cache procps && \
    rm -rf /var/cache/apk/*

RUN pip install --upgrade pip

RUN pip install poetry

ENV POETRY_NO_INTERACTION=1 \
    POETRY_NO_ROOT=1 \
    POETRY_VIRTUALENVS_IN_PROJECT=0 \
    POETRY_VIRTUALENVS_CREATE=1 \
    POETRY_VIRTUALENVS_PATH=/venv

WORKDIR /codefly


COPY bin/linux/codefly /usr/local/bin/codefly
