FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

# Base dev toolbox shared across every codefly-built image. Language
# companions either FROM this image directly (when alpine suffices) or
# `COPY --from=ghcr.io/codefly-dev/codefly` the codefly binary (when they need
# a language-specific base like Python on Alpine).
#
# Version pinning strategy:
#   - Base image: Alpine 3.24.2, pinned by digest.
#   - apk packages: the Alpine repository supplies current fixes at build
#     time. Its packages are not frozen by the base tag; the published
#     image digest identifies the resulting bytes.
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

# BuildKit supplies TARGETARCH for each platform in a multi-platform build.
# The companion publisher cross-compiles the matching static binary before
# invoking Docker, so an ARM64 image can never accidentally contain an AMD64
# Codefly executable.
ARG TARGETARCH
COPY bin/linux/${TARGETARCH}/codefly /bin/codefly

WORKDIR /workspace
