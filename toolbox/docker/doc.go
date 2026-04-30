// Package docker is the codefly Docker toolbox — image and container
// operations exposed as typed Tool RPCs.
//
// This is the canonical replacement for `bash -c "docker ..."`.
// Agents wanting to inspect images, list containers, or query a
// running container's state call typed RPCs here; the Bash toolbox
// refuses every `docker` invocation and routes callers via
// canonical_for: [docker].
//
// Implementation uses the official Docker SDK (already a core
// dependency via runners/base/docker_runner.go). No shell-out to
// /usr/bin/docker.
//
// Phase 1 ships a deliberately minimal tool set proving the contract:
// docker.list_containers, docker.list_images, docker.inspect_container.
// Mutation tools (run, exec, kill) come later — they need careful
// thinking about the boundary between "Docker toolbox runs containers
// for agents" vs "service plugins manage their own runtime via
// runners/base.DockerEnvironment." Almost certainly the toolbox will
// expose only inspection + a tightly-scoped exec; container lifecycle
// stays with service plugins.
package docker
