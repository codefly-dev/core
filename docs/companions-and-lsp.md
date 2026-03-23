# Companions and LSP

## Overview

Companions are isolated environments (Docker, Nix, or local) used to run language tooling: LSP servers (e.g. gopls), code generation (buf, swagger), and build/runtime (Go runner). All companion usage goes through the **golden wrapper** (`CompanionRunner`) so callers do not depend on a specific backend.

## Golden wrapper: CompanionRunner

- **Package:** `core/runners/base`
- **Interface:** `CompanionRunner` — `WithMount`, `WithPortMapping`, `WithWorkDir`, `WithPause`, `Init`, `NewProcess`, `Shutdown`, `RunnerEnv`, `Backend`
- **Factory:** `NewCompanionRunner(ctx, CompanionOpts)` — picks the best available backend:
  1. **Docker** — when Docker is running and an image is provided
  2. **Nix** — when `flake.nix` exists in `SourceDir` and Nix is installed
  3. **Local** — fallback (host processes, no isolation)

Containers created by the Docker backend are named with a `codefly-` prefix (e.g. `codefly-lsp-go-1234567890`). This convention is applied inside `DockerEnvironment`; callers pass a short name into `CompanionOpts.Name`.

## LSP (Language Server Protocol)

- **Package:** `core/companions/lsp`
- **Interface:** `Client` — `ListSymbols`, `NotifyChange`, `NotifySave`, `Close`
- **Factory:** `NewClient(ctx, languages.GO, sourceDir)` — uses the golden wrapper to start the language server (e.g. gopls in the Go companion), connects over TCP (JSON-RPC 2.0), runs `initialize` / `initialized`, then `waitForReady` (probes `workspace/symbol` with backoff) so the server is ready before use.

LSP is language-agnostic; each language registers a `LanguageConfig` (companion image, LSP binary, file extensions, etc.). Go is the first implementation (`lsp/go.go`).

## Proto and code generation

- **Package:** `core/companions/proto`
- Proto generation (buf, swagger) also uses `NewCompanionRunner`; the proto companion image is built and invoked via the same wrapper.

## Building companion images

From `core/`:

```bash
./companions/scripts/build_companions.sh
```

This builds:

- `codeflydev/go:<version>` — used by LSP (gopls) and Go runner
- `codeflydev/proto:<version>` — used by proto/swagger generation

Versions come from each companion’s `info.codefly.yaml`.

## Tests

- **LSP tests** (`companions/lsp`): Require Docker and the Go companion image. Use `testutil.RequireGoImage(t, ctx)`; they skip if the image is not built.
- **Proto tests** (`companions/proto`): Use `testutil.RequireProtoImage(t, ctx)`; they skip if the proto image is not built.
- **Helpers:** `core/companions/testutil` — `RequireDocker`, `RequireProtoImage`, `RequireGoImage`, and `BuildCompanionsHint`.

If a companion test fails with “No such image” or skips, build the images with the script above.
