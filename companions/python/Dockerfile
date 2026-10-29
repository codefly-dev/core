# Python runtime companion — opinionated, uv-based (no poetry).
#
# Version pinning strategy (matches codefly base):
#   - Base image: Python 3.14.10 on Alpine 3.24, pinned by digest.
#   - apk packages: not individually pinned — the Alpine repository
#     supplies current fixes at build time. The published image digest,
#     rather than the package repository, identifies the resulting bytes.
#   - uv: pinned via the install-script URL (version is part of the
#     path), bumping = editing this line + bumping info.codefly.yaml.
#   - codefly CLI: copied from the pinned codefly companion base.

# The codefly base image is a build argument: this repository declares which
# companion image it builds on, and the builder resolves that to a registry.
# The default keeps a plain `docker build` working and is pinned to the version
# in companions/codefly/info.codefly.yaml.
ARG CODEFLY_BASE_IMAGE=ghcr.io/codefly-dev/codefly:0.0.7
FROM ${CODEFLY_BASE_IMAGE} AS codeflybase

FROM python:3.14.10-alpine3.24@sha256:cfe4b5079e5852da965ae14f74a6e2b137ece05b918d27c575fb70de3c85db2b

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
COPY --from=codeflybase /bin/codefly /bin/codefly

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
RUN pip install --no-cache-dir --break-system-packages debugpy==1.8.21 && \
    python -m pip uninstall --yes --break-system-packages pip && \
    python -c 'import pathlib, shutil, sysconfig; shutil.rmtree(pathlib.Path(sysconfig.get_path("stdlib")) / "ensurepip")'

# This runtime uses uv. Remove both installed pip (including its vendored
# dependencies) and the ensurepip bootstrap wheel, which can restore the same
# vulnerable vendors. Keep debugpy and its metadata intact; inventories must
# describe the actual remaining packages, not edited vendor/SBOM declarations.
RUN python -c 'import importlib.util, debugpy.adapter; assert importlib.util.find_spec("pip") is None; assert importlib.util.find_spec("ensurepip") is None' && \
    python -m debugpy.adapter --help && \
    uv venv --offline --python /usr/local/bin/python /tmp/python-runtime-check && \
    uv pip install --no-cache --python /tmp/python-runtime-check/bin/python debugpy==1.8.21 && \
    /tmp/python-runtime-check/bin/python -c 'import debugpy; assert debugpy.__version__ == "1.8.21"' && \
    rm -rf /tmp/python-runtime-check

# uv venv convention used by python-fastapi (see DockerEnvPath in its
# runtime). Plugins mount /venv so venvs survive container restarts.
ENV UV_PROJECT_ENVIRONMENT=/venv
ENV PATH="/venv/bin:${PATH}"

WORKDIR /workspace
