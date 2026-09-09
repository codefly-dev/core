// Command buildspecs prints the companion build specs as JSON so the publish
// workflow derives every per-companion build input from companions.BuildSpecs
// instead of restating the Dockerfile paths, contexts and platforms in shell,
// where they drift from the specs without failing anything.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/codefly-dev/core/companions"
)

// entry is one companion rendered as the flat, string-valued shape a GitHub
// Actions matrix entry and a reusable workflow's inputs can carry.
type entry struct {
	Companion  string `json:"companion"`
	Version    string `json:"version"`
	Dockerfile string `json:"dockerfile"`
	Context    string `json:"context"`
	Platforms  string `json:"platforms"`
	CLIBinary  string `json:"cli_binary"`
	Base       string `json:"base"`
}

func entries(specs []companions.BuildSpec) []entry {
	out := make([]entry, 0, len(specs))
	for _, spec := range specs {
		out = append(out, entry{
			Companion:  spec.Name,
			Version:    spec.Version,
			Dockerfile: spec.Dockerfile,
			Context:    spec.Context,
			Platforms:  strings.Join(spec.Platforms, ","),
			CLIBinary:  spec.CLIBinary,
			Base:       spec.Base,
		})
	}
	return out
}

func main() {
	specs, err := companions.BuildSpecs()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.Marshal(entries(specs))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
