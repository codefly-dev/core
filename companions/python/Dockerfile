# Python runtime companion — opinionated, uv-based (no poetry).
#
# Version pinning strategy (matches codefly base):
#   - Base image: python:3.13.1-alpine3.21 — pins both Python patch and
#     Alpine minor release.
#   - apk packages: not individually pinned — Alpine point release
#     freezes its repo, so `apk add git` yields the same version at
#     every build.
#   - uv: pinned via the install-script URL (version is part of the
#     path), bumping = editing this line + bumping info.codefly.yaml.
#   - codefly CLI: copied from the pinned codeflydev/codefly:0.0.3.

FROM python:3.13.1-alpine3.21

# Re-install alpine dev tools so the python companion's runtime carries
# the same toolbox as the codefly base. Dropped from the list:
#   - less: rarely used interactively inside a plugin container
#   - build-base (gcc + make + headers): adds ~170MB for a case that's
#     increasingly rare — uv prefers prebuilt wheels, and modern Python
#     packages ship musl wheels for Alpine. Services that still depend
#     on native builds should override `docker-image:` in their settings
#     with an image that includes build-base; the trade-off is explicit.
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

# Pull the codefly CLI from the base image. `/bin/codefly` is on PATH.
COPY --from=codeflydev/codefly:0.0.3 /bin/codefly /bin/codefly

# Install uv from astral's pinned static-binary installer. Bump uv by
# editing this version path AND info.codefly.yaml together.
RUN curl -LsSf https://astral.sh/uv/0.5.29/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh

# debugpy is the DAP adapter this companion launches
# (`python -m debugpy.adapter`, see companions/dap/python.go). Pinned like the
# base image and uv above — bump deliberately, and only to a version that
# still ships a prebuilt musllinux wheel (build-base is intentionally absent,
# see the note above, so there is no compiler to build from source).
#
# Installed into the system interpreter (/usr/local/bin/python). The DAP runner
# mounts only /workspace and the uv cache — never a project venv at /venv (see
# companions/dap/client.go) — so `python` on PATH resolves here and can import
# debugpy. If a /venv is ever mounted for a debug session, the ENV PATH below
# would shadow this interpreter and the adapter must be launched via an
# absolute /usr/local/bin/python instead.
#
# --break-system-packages is required because the alpine base marks the system
# env as externally managed.
RUN pip install --no-cache-dir --break-system-packages debugpy==1.8.21

# uv venv convention used by python-fastapi (see DockerEnvPath in its
# runtime). Plugins mount /venv so venvs survive container restarts.
ENV UV_PROJECT_ENVIRONMENT=/venv
ENV PATH="/venv/bin:${PATH}"

WORKDIR /workspace
