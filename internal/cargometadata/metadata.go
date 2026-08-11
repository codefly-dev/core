// Package cargometadata owns Codefly's typed boundary to Cargo's authoritative
// project graph. Consumers share one command and JSON contract instead of
// independently parsing Cargo.toml or inventing language metadata.
package cargometadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrUnavailable reports that the real Cargo toolchain is absent.
var ErrUnavailable = errors.New("cargo is required")

// Options controls read-only Cargo metadata collection.
type Options struct {
	ManifestPath   string
	Locked         bool
	NoDependencies bool
	Offline        bool
}

// Metadata is Cargo metadata format-version 1 with the fields shared by
// project inspection and SBOM graph construction.
type Metadata struct {
	Packages         []Package `json:"packages"`
	Resolve          *Resolve  `json:"resolve"`
	WorkspaceMembers []string  `json:"workspace_members"`
	WorkspaceRoot    string    `json:"workspace_root"`
}

// Resolve is Cargo's resolved dependency graph.
type Resolve struct {
	Root  *string `json:"root"`
	Nodes []Node  `json:"nodes"`
}

// Package is one Cargo package declaration.
type Package struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	ID           string       `json:"id"`
	Source       *string      `json:"source"`
	License      *string      `json:"license"`
	Checksum     *string      `json:"checksum"`
	ManifestPath string       `json:"manifest_path"`
	Edition      string       `json:"edition"`
	Dependencies []Dependency `json:"dependencies"`
	Targets      []Target     `json:"targets"`
}

// Dependency is a direct Cargo manifest dependency.
type Dependency struct {
	Name   string  `json:"name"`
	Source *string `json:"source"`
	Req    string  `json:"req"`
	Kind   *string `json:"kind"`
	Rename *string `json:"rename"`
	Path   *string `json:"path"`
}

// Target is a Cargo package build target and its source entry point.
type Target struct {
	Name    string   `json:"name"`
	Kind    []string `json:"kind"`
	SrcPath string   `json:"src_path"`
	Edition string   `json:"edition"`
}

// Node is one package in Cargo's resolved dependency graph.
type Node struct {
	ID   string           `json:"id"`
	Deps []NodeDependency `json:"deps"`
}

// NodeDependency is one resolved package edge.
type NodeDependency struct {
	Pkg      string           `json:"pkg"`
	DepKinds []DependencyKind `json:"dep_kinds"`
}

// DependencyKind distinguishes normal, build, and development graph edges.
type DependencyKind struct {
	Kind *string `json:"kind"`
}

// Run executes Cargo's non-mutating metadata API and decodes its typed result.
func Run(ctx context.Context, dir string, options Options) (Metadata, error) {
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		return Metadata{}, ErrUnavailable
	}
	args := []string{"metadata", "--format-version", "1"}
	if options.ManifestPath != "" {
		manifestPath, err := filepath.Abs(options.ManifestPath)
		if err != nil {
			return Metadata{}, fmt.Errorf("resolve Cargo manifest path: %w", err)
		}
		args = append(args, "--manifest-path", manifestPath)
	}
	if options.Locked {
		args = append(args, "--locked")
	}
	if options.NoDependencies {
		args = append(args, "--no-deps")
	}
	if options.Offline {
		args = append(args, "--offline")
	}
	cmd := exec.CommandContext(ctx, cargo, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Metadata{}, fmt.Errorf("cargo %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return Parse(stdout.Bytes())
}

// Parse decodes Cargo metadata format-version 1.
func Parse(data []byte) (Metadata, error) {
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("parse cargo metadata: %w", err)
	}
	return metadata, nil
}
