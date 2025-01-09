# First stage: Build environment
FROM golang:1.22-alpine AS builder

# Install dependencies
RUN apk update inutils build-base linux-headers gcc g++ git
RUN apk add --no-cache protobuf protobuf-dev
RUN apk add --no-cache python3 py3-pip python3-dev py3-grpcio
RUN apk add --no-cache nodejs npm

RUN go install github.com/bufbuild/buf/cmd/buf@latest

# Create a Python virtual environment and install protoc plugins for Python
RUN python3 -m venv /venv
RUN /venv/bin/pip install "betterproto[compiler]"

# TypeScript protoc
RUN npm install -g @bufbuild/protoc-gen-connect-web @bufbuild/protoc-gen-es @bufbuild/protoc-gen-connect-es

# go gRPC Gateway protoc
RUN go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest

# go gRPC Gateway openapi
RUN go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

# Swagger
RUN go install github.com/go-swagger/go-swagger/cmd/swagger@latest

# Validate protoc
RUN go install github.com/envoyproxy/protoc-gen-validate@latest

# Typescript protoc
RUN npm install -g ts-proto @types/node typescript


# Second stage: Create the runtime image
FROM golang:alpine

# Add Python/Node.js runtime
RUN apk add --no-cache python3 nodejs npm

# Copy necessary binaries and files from the builder stage
COPY --from=builder /usr/bin/protoc /usr/local/bin/
COPY --from=builder /usr/bin/protoc* /usr/local/bin/
COPY --from=builder /usr/lib/libprotoc.so* /usr/lib/
COPY --from=builder /usr/lib/libprotobuf.so* /usr/lib/
COPY --from=builder /go/bin/buf /usr/local/bin/

COPY --from=builder /go/bin/protoc-gen-grpc-gateway /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-openapiv2 /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-validate /usr/local/bin/
COPY --from=builder /go/bin/swagger /usr/local/bin/

COPY --from=builder /usr/bin/grpc_* /usr/local/bin/

COPY --from=builder /usr/local/lib/node_modules/@bufbuild/protoc-gen-*/bin/* /usr/local/bin/

COPY --from=builder /usr/local/bin/protoc-gen-ts_proto /usr/local/bin/
COPY --from=builder /usr/local/lib/node_modules /usr/local/lib/node_modules

COPY --from=builder /venv /venv


COPY --from=builder /usr/lib/libabsl*.so* /usr/lib/


ENV PATH="/venv/bin:${PATH}"

# Set the working directory
WORKDIR /workspace
