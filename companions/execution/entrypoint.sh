#!/usr/bin/env bash
set -Eeuo pipefail

log() {
  printf '[execution] %s\n' "$*" >&2
}

die() {
  log "$*"
  exit 1
}

is_empty_dir() {
  local dir="$1"
  [ -d "$dir" ] || return 0
  [ -z "$(find "$dir" -mindepth 1 -maxdepth 1 -print -quit)" ]
}

wait_for_docker() {
  local deadline="${1:-60}"
  local waited=0
  until docker info >/dev/null 2>&1; do
    if [ "$waited" -ge "$deadline" ]; then
      return 1
    fi
    sleep 1
    waited=$((waited + 1))
  done
  return 0
}

start_docker_if_requested() {
  local mode="${CODEFLY_EXECUTION_DOCKER:-auto}"
  case "$mode" in
    off|disabled|false|none)
      log "Docker daemon startup disabled"
      return 0
      ;;
    external)
      wait_for_docker "${CODEFLY_DOCKER_WAIT_SECONDS:-60}" || die "external Docker daemon is not reachable at ${DOCKER_HOST:-default}"
      log "Docker daemon available (${DOCKER_HOST:-default})"
      return 0
      ;;
  esac

  if docker info >/dev/null 2>&1; then
    log "Docker daemon already available (${DOCKER_HOST:-default})"
    return 0
  fi

  if [ "$mode" = "rootless" ]; then
    die "rootless Docker mode is not bundled in this image yet; run with CODEFLY_EXECUTION_DOCKER=auto in a privileged container or provide an external daemon"
  fi

  mkdir -p /var/log /var/run
  log "starting Docker daemon"
  dockerd-entrypoint.sh --host=unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &
  wait_for_docker "${CODEFLY_DOCKER_WAIT_SECONDS:-90}" || {
    log "Docker daemon did not become ready; last log lines:"
    tail -80 /var/log/dockerd.log >&2 || true
    die "Docker daemon startup failed"
  }
  log "Docker daemon ready"
}

checkout_ref() {
  local root="$1"
  local ref="${CODEFLY_REPO_REF:-${CODEFLY_REPO_BRANCH:-}}"
  [ -n "$ref" ] || return 0
  git -C "$root" fetch --tags --prune origin "$ref" >/dev/null 2>&1 || true
  git -C "$root" checkout "$ref"
}

prepare_workspace() {
  local root="${CODEFLY_EXECUTION_ROOT:-/workspace}"
  mkdir -p "$root"

  if [ -n "${CODEFLY_REPO_URL:-}" ]; then
    if [ -d "$root/.git" ]; then
      log "workspace already has a git checkout"
      checkout_ref "$root"
    elif is_empty_dir "$root"; then
      log "cloning ${CODEFLY_REPO_URL} into ${root}"
      git clone --filter=blob:none "$CODEFLY_REPO_URL" "$root" || git clone "$CODEFLY_REPO_URL" "$root"
      checkout_ref "$root"
    else
      log "workspace is not empty and has no .git; skipping clone"
    fi
  fi

  maybe_write_mind_yaml "$root"
  EXECUTION_WORKSPACE_ROOT="$root"
}

detect_plugin() {
  local root="$1"
  if [ -f "$root/go.mod" ]; then
    printf 'go-generic go\n'
  elif [ -f "$root/pyproject.toml" ] || [ -f "$root/setup.py" ] || [ -f "$root/requirements.txt" ] || [ -f "$root/tox.ini" ]; then
    printf 'python-generic python\n'
  elif [ -f "$root/package.json" ]; then
    printf 'node-generic node\n'
  elif [ -f "$root/Cargo.toml" ]; then
    printf 'rust-generic rust\n'
  else
    printf 'python-generic generic\n'
  fi
}

maybe_write_mind_yaml() {
  local root="$1"
  if [ -f "$root/mind.yaml" ]; then
    return 0
  fi
  if [ "${CODEFLY_GENERATE_MIND_YAML:-1}" = "0" ] || [ "${CODEFLY_GENERATE_MIND_YAML:-1}" = "false" ]; then
    return 0
  fi

  local service="${CODEFLY_SERVICE_NAME:-workspace}"
  local plugin="${CODEFLY_SERVICE_PLUGIN:-}"
  local lang="${CODEFLY_SERVICE_TYPE:-}"
  if [ -z "$plugin" ] || [ -z "$lang" ]; then
    read -r detected_plugin detected_lang < <(detect_plugin "$root")
    plugin="${plugin:-$detected_plugin}"
    lang="${lang:-$detected_lang}"
  fi

  log "writing mind.yaml service=${service} plugin=${plugin} type=${lang}"
  cat > "$root/mind.yaml" <<YAML
service: ${service}
plugin: ${plugin}
config:
  path: .
  type: ${lang}
YAML
}

run_gateway() {
  local root="$1"
  local port="${CODEFLY_GATEWAY_PORT:-50051}"
  export CODEFLY_GATEWAY_HOST="${CODEFLY_GATEWAY_HOST:-0.0.0.0}"
  log "starting codefly Gateway on ${CODEFLY_GATEWAY_HOST}:${port} dir=${root}"
  exec codefly daemon gateway --dir "$root" --port "$port"
}

main() {
  if [ "$#" -gt 0 ] && [ "$1" != "gateway" ]; then
    exec "$@"
  fi
  if [ "${1:-}" = "gateway" ]; then
    shift
  fi

  start_docker_if_requested
  prepare_workspace
  run_gateway "$EXECUTION_WORKSPACE_ROOT"
}

main "$@"
