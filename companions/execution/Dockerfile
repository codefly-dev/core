FROM docker:28-dind

# Remote execution companion.
#
# This image is the local/Docker stand-in for the future Kubernetes sandbox pod:
# it runs a codefly Gateway over gRPC, has a Docker daemon available for nested
# builds/tests, and includes Nix so per-repo environments can be made explicit.
#
# The codefly CLI is copied from core/bin/linux/codefly, produced by
# `codefly companion build execution` before docker build starts.
RUN apk update && \
    apk add --no-cache \
        bash \
        ca-certificates \
        coreutils \
        curl \
        findutils \
        g++ \
        gcc \
        git \
        go \
        jq \
        linux-headers \
        make \
        musl-dev \
        nix \
        nodejs \
        npm \
        openssh-client \
        patch \
        procps \
        py3-pip \
        py3-virtualenv \
        python3 \
        ripgrep \
        shadow \
        tini \
        tzdata \
        unzip \
        uv \
        xz \
        zip \
    && rm -rf /var/cache/apk/*

COPY bin/linux/codefly /bin/codefly
COPY companions/execution/entrypoint.sh /usr/local/bin/codefly-execution-entrypoint

RUN chmod +x /usr/local/bin/codefly-execution-entrypoint && \
    mkdir -p /etc/nix /workspace /var/log && \
    printf '%s\n' \
        'experimental-features = nix-command flakes' \
        'sandbox = false' \
        > /etc/nix/nix.conf

ENV CODEFLY_GATEWAY_HOST=0.0.0.0
ENV CODEFLY_GATEWAY_PORT=50051
ENV CODEFLY_EXECUTION_ROOT=/workspace
ENV CODEFLY_EXECUTION_DOCKER=auto
ENV DOCKER_HOST=unix:///var/run/docker.sock
ENV DOCKER_TLS_CERTDIR=
ENV NIX_CONFIG="experimental-features = nix-command flakes"
ENV PATH="/root/.nix-profile/bin:/nix/var/nix/profiles/default/bin:${PATH}"

EXPOSE 50051
WORKDIR /workspace
ENTRYPOINT ["tini", "--", "/usr/local/bin/codefly-execution-entrypoint"]
CMD ["gateway"]
