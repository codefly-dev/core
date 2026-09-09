# The codefly base image is a build argument: this repository declares which
# companion image it builds on, and the builder resolves that to a registry.
# The default keeps a plain `docker build` working and is pinned to the version
# in companions/codefly/info.codefly.yaml.
ARG CODEFLY_BASE_IMAGE=ghcr.io/codefly-dev/codefly:0.0.4
FROM ${CODEFLY_BASE_IMAGE} AS codeflybase

FROM golang:1.27-alpine

# Dev toolbox kept minimal (Go agent only uses git for go-module fetches
# and a shell for diagnostics). Language companions share the same
# apk lineup as the codefly base for consistency.
RUN apk update && \
    apk add --no-cache \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        procps \
        tzdata \
    && rm -rf /var/cache/apk/*

# codefly CLI from the pinned base image. Matches python/node companions
# so users get the same CLI + tool set regardless of language.
COPY --from=codeflybase /bin/codefly /bin/codefly

WORKDIR /workspace
