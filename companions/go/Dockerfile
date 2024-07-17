FROM golang:1.22-alpine

RUN apk update && \
    apk add --no-cache procps && \
    rm -rf /var/cache/apk/*

COPY bin/linux/codefly /bin/codefly
WORKDIR /codefly
