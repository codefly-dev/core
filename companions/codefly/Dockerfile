FROM alpine:3.21.3

# Base dev toolbox shared across every codefly-built image. Language
# companions either FROM this image directly (when alpine suffices) or
# `COPY --from=codeflydev/codefly` the codefly binary (when they need
# a language-specific base like python:3.13.1-alpine3.21).
#
# Version pinning strategy:
#   - Base image: pinned to alpine:3.21.3 (specific patch, not :latest).
#   - apk packages: not individually pinned. Alpine point releases
#     freeze their package repo, so `apk add git` from alpine:3.21.3
#     always yields the same git build. Pinning every apk line is
#     brittle (versions shift between minor releases) and delivers no
#     reproducibility benefit beyond what the base tag gives us.
#   - codefly CLI: copied from a locally-built binary, not a tag.
#
# Keep minimal. Heavy tools (language runtimes, LSP servers, proto
# plugins) belong in language-specific companions, not here.
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

# codefly CLI itself. Built separately via cli/scripts/build/linux.sh
# and placed under core/bin/linux/codefly before the companion build.
COPY bin/linux/codefly /bin/codefly

WORKDIR /workspace
