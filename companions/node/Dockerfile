# Node runtime companion for the nextjs plugin (and future node-based
# plugins). Pinned like every other codefly-built image — no :latest.
#
# Version pinning strategy (matches the codefly + python companions):
#   - Base: node:24.16.0-alpine3.22 — Node LTS + matching Alpine release.
#   - apk packages: not individually pinned; Alpine release freezes repo.
#   - codefly CLI: copied from the pinned codeflydev/codefly base.

FROM node:24.16.0-alpine3.22

# Re-install alpine dev tools so the node companion carries the same
# toolbox as the codefly base.
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

# Pull the Codefly CLI from the multi-platform base image. BuildKit resolves
# the base manifest for the Node image's target architecture.
COPY --from=codeflydev/codefly:0.0.4 /bin/codefly /bin/codefly

WORKDIR /workspace
