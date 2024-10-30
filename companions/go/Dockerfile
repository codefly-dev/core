FROM golang:1.23-alpine

RUN apk update && \
    apk add --no-cache procps && \
    rm -rf /var/cache/apk/*

COPY bin/linux/codefly /bin/codefly
WORKDIR /codefly
