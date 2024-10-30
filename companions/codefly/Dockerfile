FROM alpine:3.20

RUN apk update && \
    apk add --no-cache procps && \
    rm -rf /var/cache/apk/*



COPY bin/linux/codefly /bin/codefly
