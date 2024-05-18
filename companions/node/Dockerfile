FROM node:21-alpine

RUN apk update && \
    apk add --no-cache procps && \
    rm -rf /var/cache/apk/*

RUN adduser -D codefly
USER codefly

WORKDIR /codefly
