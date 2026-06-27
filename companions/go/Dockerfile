FROM golang:1.26-alpine

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
COPY --from=codeflydev/codefly:0.0.3 /bin/codefly /bin/codefly

WORKDIR /workspace
