// Package base is the runner foundation: it defines the RunnerEnvironment
// and Proc abstractions and provides three backends — Native, Docker,
// and Nix — plus the CompanionRunner wrapper that picks the best
// backend at runtime.
//
// All higher-level runners (runners/golang, runners/python) build on
// these primitives. Process supervision (process group setup, tree-kill,
// orphan reaping via pgid files) lives here so every backend behaves
// identically under SIGTERM and ctx-cancel.
package base
