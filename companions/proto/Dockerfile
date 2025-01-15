FROM alpine:latest

# Install only the necessary dependencies in one layer
RUN apk update && apk add --no-cache \
    protobuf \
    protobuf-dev \
    python3 \
    py3-pip \
    nodejs \
    npm \
    wget \
    go && \
    # Download and install binaries
    wget https://github.com/bufbuild/buf/releases/download/v1.9.0/buf-Linux-aarch64 -O /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf && \
    wget https://github.com/grpc-ecosystem/grpc-gateway/releases/download/v2.19.1/protoc-gen-grpc-gateway-v2.19.1-linux-arm64 -O /usr/local/bin/protoc-gen-grpc-gateway && \
    chmod +x /usr/local/bin/protoc-gen-grpc-gateway && \
    wget https://github.com/grpc-ecosystem/grpc-gateway/releases/download/v2.19.1/protoc-gen-openapiv2-v2.19.1-linux-arm64 -O /usr/local/bin/protoc-gen-openapiv2 && \
    chmod +x /usr/local/bin/protoc-gen-openapiv2 && \
    wget https://github.com/go-swagger/go-swagger/releases/download/v0.30.5/swagger_linux_arm64 -O /usr/local/bin/swagger && \
    chmod +x /usr/local/bin/swagger && \
    # Install Go tools
    GOBIN=/usr/local/bin go install github.com/envoyproxy/protoc-gen-validate@v1.0.4 && \
    # Python setup
    python3 -m venv /venv && \
    /venv/bin/pip install --no-cache-dir "betterproto[compiler]==2.0.0b6" && \
    # TypeScript setup
    cd /tmp && \
    npm init -y && \
    npm install --legacy-peer-deps --no-package-lock ts-proto@1.167.3 && \
    ln -sf /tmp/node_modules/.bin/protoc-gen-ts_proto /usr/local/bin/ && \
    # Cleanup
    rm -rf /root/.cache /root/.npm /root/go && \
    apk del wget go

ENV PATH="/venv/bin:/usr/local/bin:/tmp/node_modules/.bin:${PATH}"

WORKDIR /workspace
