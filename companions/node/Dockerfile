FROM node:22-alpine

RUN apk update && \
    apk add --no-cache procps && \
    rm -rf /var/cache/apk/*

WORKDIR /codefly
