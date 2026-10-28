// Command buildspecs prints the companion build specs as JSON so the publish
// workflow derives every per-companion build input from companions.BuildSpecs
// instead of restating the Dockerfile paths, contexts and platforms in shell,
// where they drift from the specs without failing anything.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/codefly-dev/core/companions"
)

// entry is one companion rendered as the flat, string-valued shape a GitHub
// Actions matrix entry and a reusable workflow's inputs can carry.
type entry struct {
	Companion     string `json:"companion"`
	Version       string `json:"version"`
	Dockerfile    string `json:"dockerfile"`
	Context       string `json:"context"`
	Platforms     string `json:"platforms"`
	CLIBinary     string `json:"cli_binary"`
	Base          string `json:"base"`
	BaseCLIBinary string `json:"base_cli_binary"`
}

func entries(specs []companions.BuildSpec) []entry {
	cliBinaries := make(map[string]string, len(specs))
	for _, spec := range specs {
		cliBinaries[spec.Name] = spec.CLIBinary
	}
	out := make([]entry, 0, len(specs))
	for _, spec := range specs {
		out = append(out, entry{
			Companion:     spec.Name,
			Version:       spec.Version,
			Dockerfile:    spec.Dockerfile,
			Context:       spec.Context,
			Platforms:     strings.Join(spec.Platforms, ","),
			CLIBinary:     spec.CLIBinary,
			Base:          spec.Base,
			BaseCLIBinary: cliBinaries[spec.Base],
		})
	}
	return out
}

func main() {
	bake := flag.Bool("bake", false, "emit a Buildx Bake graph with companion bases built from this checkout")
	flag.Parse()
	specs, err := companions.BuildSpecs()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var output any = entries(specs)
	if *bake {
		output = bakeFile(specs)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

type bakeTarget struct {
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile"`
	Platforms  []string          `json:"platforms"`
	Contexts   map[string]string `json:"contexts,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
}

// A target context builds the declared base from the same checkout. A release
// PR can therefore qualify a new base before its tag exists in the registry.
func bakeFile(specs []companions.BuildSpec) map[string]map[string]bakeTarget {
	targets := make(map[string]bakeTarget, len(specs))
	for _, spec := range specs {
		target := bakeTarget{Context: spec.Context, Dockerfile: spec.Dockerfile, Platforms: spec.Platforms}
		if spec.Base != "" {
			target.Contexts = map[string]string{spec.Base: "target:" + spec.Base}
			target.Args = map[string]string{companions.BaseImageArg: spec.Base}
		}
		targets[spec.Name] = target
	}
	return map[string]map[string]bakeTarget{"target": targets}
}
