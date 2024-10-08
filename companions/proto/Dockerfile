# First stage: Build environment
FROM golang:1.22-alpine AS builder

# Install dependencies
RUN apk add --no-cache git python3 py3-pip nodejs npm protobuf

RUN go install github.com/bufbuild/buf/cmd/buf@latest

# Create a Python virtual environment and install protoc plugins for Python
RUN python3 -m venv /venv
RUN /venv/bin/pip install grpcio-tools

# Install protoc plugins for TypeScript
RUN npm install -g ts-protoc-gen

# Install protoc plugins for gRPC Gateway and OpenAPI
RUN go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest

RUN go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

RUN go install github.com/go-swagger/go-swagger/cmd/swagger@latest

# Install protoc plugin for validation
RUN go install github.com/envoyproxy/protoc-gen-validate@latest


# Second stage: Create the runtime image
FROM golang:alpine

# Copy necessary binaries and files from the builder stage
COPY --from=builder /usr/bin/protoc /usr/local/bin/
COPY --from=builder /go/bin/buf /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-grpc-gateway /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-openapiv2 /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-validate /usr/local/bin/
COPY --from=builder /usr/bin/grpc_* /usr/local/bin/
COPY --from=builder /go/bin/swagger /usr/local/bin/
# COPY --from=builder /usr/bin/ts-protoc-gen /usr/local/bin/

# Set the working directory
WORKDIR /workspace
