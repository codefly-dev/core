FROM alpine:3.14

RUN apk update && \
    apk add --no-cache procps && \
    rm -rf /var/cache/apk/*



COPY bin/codefly /usr/local/bin/codefly
