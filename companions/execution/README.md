# codefly execution companion

This companion is the local Docker proof for Mind remote Gateway mode. It runs
`codefly daemon gateway` in a container, exposes `mind.gateway.v1.Gateway` on
port `50051`, and keeps Docker plus Nix available inside the execution cell.

Build:

```sh
codefly companion build execution --core-dir /path/to/codefly.dev/core
```

Run against a mounted workspace:

```sh
export CODEFLY_GATEWAY_TOKEN="$(openssl rand -hex 32)"
docker run --rm --privileged \
  -p 50051:50051 \
  -v "$PWD:/workspace" \
  -e CODEFLY_GATEWAY_TOKEN \
  codeflydev/execution:0.0.1
```

Run by cloning a repo:

```sh
docker run --rm --privileged \
  -p 50051:50051 \
  -e CODEFLY_GATEWAY_TOKEN \
  -e CODEFLY_REPO_URL=https://github.com/pallets/flask.git \
  -e CODEFLY_REPO_REF=main \
  codeflydev/execution:0.0.1
```

Mind local proof:

```sh
WORKSPACE_RUNTIME=docker \
WORKSPACE_ENDPOINT=127.0.0.1:50051 \
MIND_REMOTE_GATEWAY=1 \
CODEFLY_GATEWAY_TOKEN="$CODEFLY_GATEWAY_TOKEN" \
mind serve
```

The image generates a minimal `mind.yaml` when one is missing. Set
`CODEFLY_GENERATE_MIND_YAML=0` to disable that behavior, or set
`CODEFLY_SERVICE_NAME`, `CODEFLY_SERVICE_PLUGIN`, and `CODEFLY_SERVICE_TYPE` to
override the detected values.

The gateway refuses a non-loopback bind unless `CODEFLY_GATEWAY_TOKEN` is set.
Clients must send the value as `authorization: Bearer <token>` or in the
`x-codefly-gateway-token` gRPC metadata field.
