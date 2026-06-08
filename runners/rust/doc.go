// Package rust is the Rust-specific runner: build, test, and run Cargo
// projects in any of the supported execution environments (native,
// Docker, Nix). It mirrors core/runners/golang — same public shape, same
// base environments (NativeEnvironment, NixEnvironment, CompanionRunner) —
// swapping the Go toolchain for Cargo:
//
//	go build            → cargo build [--release]
//	go test -json       → cargo test  (libtest text output, parsed here)
//	go vet              → cargo clippy
//	go mod download     → cargo fetch
//	go.mod / go.sum     → Cargo.toml / Cargo.lock
//	GOMODCACHE / GOPATH → CARGO_HOME
package rust
