FROM golang:1.26-alpine

# Dev toolbox kept minimal (Go agent only uses git for go-module fetches
# and a shell for diagnostics). Language companions share the same
# apk lineup as the codefly base for consistency.
RUN apk update && \
    apk add --no-cache \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        procps \
        tzdata \
    && rm -rf /var/cache/apk/*

# codefly CLI from the pinned base image. Matches python/node companions
# so users get the same CLI + tool set regardless of language.
COPY --from=codeflydev/codefly:0.0.3 /bin/codefly /bin/codefly

# gopls for LSP symbol queries. Installing in one RUN with:
#   -ldflags="-s -w"  — strip symbols, ~20% smaller binary
#   rm -rf $GOMODCACHE + $GOCACHE — `go install` leaves behind hundreds
#     of MB of downloaded source + build cache in /go/pkg/mod and
#     /root/.cache/go-build. The binary is in /go/bin; everything else
#     is scaffolding and can go. This single cleanup drops ~400 MB.
RUN go install -ldflags="-s -w" golang.org/x/tools/gopls@latest && \
    rm -rf /go/pkg/mod /root/.cache/go-build

WORKDIR /workspace
