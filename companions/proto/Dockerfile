# Base layer with essential tools
FROM alpine:latest as base
RUN apk update && apk add --no-cache \
    git \
    protobuf \
    protobuf-dev \
    python3 \
    py3-pip \
    nodejs \
    npm \
    wget \
    go

# Binary tools layer
FROM base as binary-tools
RUN wget https://github.com/bufbuild/buf/releases/download/v1.50.0/buf-Linux-aarch64 -O /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf && \
    wget https://github.com/grpc-ecosystem/grpc-gateway/releases/download/v2.26.1/protoc-gen-grpc-gateway-v2.26.1-linux-arm64 -O /usr/local/bin/protoc-gen-grpc-gateway && \
    chmod +x /usr/local/bin/protoc-gen-grpc-gateway && \
    wget https://github.com/grpc-ecosystem/grpc-gateway/releases/download/v2.26.1/protoc-gen-openapiv2-v2.26.1-linux-arm64 -O /usr/local/bin/protoc-gen-openapiv2 && \
    chmod +x /usr/local/bin/protoc-gen-openapiv2 && \
    wget https://github.com/go-swagger/go-swagger/releases/download/v0.31.0/swagger_linux_arm64 -O /usr/local/bin/swagger && \
    chmod +x /usr/local/bin/swagger

# Go tools layer
FROM binary-tools as go-tools
RUN GOBIN=/usr/local/bin go install github.com/envoyproxy/protoc-gen-validate@v1.2.1 && \
    GOBIN=/usr/local/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5 && \
    GOBIN=/usr/local/bin go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0

# Python setup layer
FROM go-tools as python-setup
RUN python3 -m venv /venv && \
    /venv/bin/pip install --no-cache-dir "betterproto[compiler]==v2.0.0-beta7"

# TypeScript setup layer
FROM python-setup as typescript-setup
RUN cd /tmp && \
    npm init -y && \
    npm install --legacy-peer-deps --no-package-lock ts-proto@2.6.1 && \
    ln -sf /tmp/node_modules/.bin/protoc-gen-ts_proto /usr/local/bin/

# Final layer
FROM typescript-setup
RUN rm -rf /root/.cache /root/.npm /root/go && \
    apk del wget

ENV PATH="/venv/bin:/usr/local/bin:/tmp/node_modules/.bin:${PATH}"
ENV GOBIN=/usr/local/bin

WORKDIR /workspace
