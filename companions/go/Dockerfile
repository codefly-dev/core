FROM golang:1.26-alpine

RUN apk update && \
    apk add --no-cache git procps && \
    rm -rf /var/cache/apk/*

# Install gopls for LSP symbol queries
RUN go install golang.org/x/tools/gopls@latest

WORKDIR /workspace
