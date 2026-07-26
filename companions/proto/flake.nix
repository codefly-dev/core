# Proto companion image, built from Nix.
#
# Replaces the hand-rolled multi-stage Dockerfile. Key advantages:
#
#   - Reproducible: Nix pins every transitive dependency by content
#     hash via flake.lock. Two builds from the same commit produce
#     bit-identical images. The Dockerfile pinned `golang:1.26-alpine`
#     and `apk add` package versions on a best-effort basis only.
#
#   - Layered cache: dockerTools.streamLayeredImage produces an image
#     where each store path is its own layer. Bumping one tool (say,
#     buf) doesn't invalidate the layers for protoc, gofmt, swagger,
#     etc. — registry pull deltas are typically a single layer rather
#     than the full image.
#
#   - One source of truth: the dev shell agents use locally and the
#     OCI image they push are the same package set, by construction.
#     No more drift between "works on my machine" (Dockerfile) and
#     "tools the agent flake exposes."
#
# Build:
#   nix build .#dockerImage
#   nix run .#streamDockerImage | docker load
#
# On macOS, building Linux images requires nix-darwin's linux-builder
# (declarative cross-builder) or an OrbStack/Lima Linux VM. Pure
# x86_64-darwin can't produce a linux/amd64 OCI image directly.
#
# Image targets the same tag the existing companion expects:
# codeflydev/proto:<version-from-info.codefly.yaml>.
{
  description = "codefly proto companion image — built from Nix";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" ] (system:
      let
        pkgs = import nixpkgs { inherit system; };

        # protoc-gen-es must match the @bufbuild/protobuf runtime version
        # consumers pin (currently 2.11.0). nixpkgs ships a different
        # version, so build it from the pinned protobuf-es source rather
        # than taking pkgs.protoc-gen-es: a companion es that diverges from
        # a repo's pinned runtime makes every committed *_pb.ts drift on
        # sync-drift. Keep in lockstep with the Dockerfile pin and the
        # runtime — see issue #83.
        protocGenEsVersion = "2.11.0";
        protoc-gen-es = pkgs.buildNpmPackage {
          pname = "protoc-gen-es";
          version = protocGenEsVersion;
          src = pkgs.fetchFromGitHub {
            owner = "bufbuild";
            repo = "protobuf-es";
            rev = "v${protocGenEsVersion}";
            hash = "sha256-iOzqBzeABRFH0KExn2ZHy6h7iiki0Ze93VpooW0MtK4=";
          };
          npmDepsHash = "sha256-vCOlDuvcHju9T9uDve9/MgID5cELL+vNRG6PxfyaAvA=";
          npmWorkspace = "packages/protoc-gen-es";
          preBuild = ''
            npm run --workspace=packages/protobuf build
            npm run --workspace=packages/protoplugin build
          '';
          # buf's workspace layout leaves dangling node_modules symlinks
          # into unbuilt sibling packages; deref the ones the plugin needs
          # so the output has no broken links.
          postInstall = ''
            rm -rf $out/lib/node_modules/protobuf-es/node_modules/ts4.*
            cp -rL node_modules/ts4.* $out/lib/node_modules/protobuf-es/node_modules/

            rm -rf $out/lib/node_modules/protobuf-es/node_modules/ts5.*
            cp -rL node_modules/ts5.* $out/lib/node_modules/protobuf-es/node_modules/

            rm -rf $out/lib/node_modules/protobuf-es/node_modules/upstream-protobuf
            cp -rL node_modules/upstream-protobuf $out/lib/node_modules/protobuf-es/node_modules/

            rm -rf $out/lib/node_modules/protobuf-es/node_modules/@bufbuild
            cp -rL node_modules/@bufbuild $out/lib/node_modules/protobuf-es/node_modules/
          '';
          dontStrip = true;
          meta.mainProgram = "protoc-gen-es";
        };

        # Tools the proto companion exposes at runtime. Same set as
        # the Dockerfile installs — only the source-of-pinning differs
        # (apk → nixpkgs).
        protoTools = with pkgs; [
          # Core build chain
          buf
          protobuf
          # gRPC-Gateway plugins (built into nixpkgs as separate
          # packages; the Dockerfile builds these from source via
          # `go install`).
          protoc-gen-go
          protoc-gen-go-grpc
          protoc-gen-grpc-gateway
          protoc-gen-openapiv2
          protoc-gen-connect-go
          # Swagger client generator — needs `go` in PATH at runtime
          # to format its output, hence go below.
          go-swagger
          # Bring `go`/`gofmt` for swagger's source formatter.
          go
          # Python grpcio-tools — for grpc_python_plugin et al.
          (python3.withPackages (ps: with ps; [ grpcio-tools ]))
          # protoc-gen-es, version-pinned to the ecosystem runtime (built
          # above from source, not pkgs.protoc-gen-es).
          protoc-gen-es
          # Node + npm for the npx-driven TypeScript generators
          # (openapi-typescript, swagger2openapi).
          nodejs_22
          # Shell + coreutils so `bash -c '...'` style invocations
          # from the codefly host work inside the container.
          bash
          coreutils
        ];

        # Read version from companion's info.codefly.yaml so the
        # image tag stays in sync with the existing version-bump
        # workflow. flake-utils doesn't help with file-relative reads;
        # we hardcode the path.
        version = builtins.head (
          builtins.match "version: ([^\n]+).*"
            (builtins.readFile ./info.codefly.yaml));

        dockerImage = pkgs.dockerTools.buildLayeredImage {
          name = "codeflydev/proto";
          tag = version;

          contents = protoTools;

          config = {
            WorkingDir = "/workspace";
            Env = [
              # /workspace is where the codefly host bind-mounts the
              # source tree.
              "PATH=/bin:/usr/bin"
              "HOME=/tmp"
            ];
            # No CMD — the codefly host always runs an explicit
            # command via NewProcess. ENTRYPOINT-less is right.
          };
        };

        # Stream variant: doesn't write the OCI tarball to the Nix
        # store, pipes to stdout instead. Major IO/disk win for
        # large images on the build machine.
        streamDockerImage = pkgs.dockerTools.streamLayeredImage {
          name = "codeflydev/proto";
          tag = version;
          contents = protoTools;
          config = {
            WorkingDir = "/workspace";
            Env = [
              "PATH=/bin:/usr/bin"
              "HOME=/tmp"
            ];
          };
        };
      in {
        packages = {
          default = dockerImage;
          inherit dockerImage streamDockerImage;
        };

        # Dev shell agents use to run buf etc. interactively. Same
        # package set as the image — no drift.
        devShells.default = pkgs.mkShell {
          packages = protoTools;
        };
      });
}
