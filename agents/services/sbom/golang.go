package sbom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type goModule struct {
	Path    string    `json:"Path"`
	Version string    `json:"Version"`
	Main    bool      `json:"Main"`
	Replace *goModule `json:"Replace,omitempty"`
}

// Golang inventories the exact module graph selected by go.mod/go.sum without
// mutating either file. GOWORK is disabled so an ambient developer workspace
// cannot silently change the released service SBOM.
func Golang(ctx context.Context, dir string) (*Result, error) {
	modulesOut, err := runGo(ctx, dir, "list", "-mod=readonly", "-m", "-json", "all")
	if err != nil {
		return nil, fmt.Errorf("go module inventory: %w", err)
	}
	modules, root, byToken, err := parseGoModules(modulesOut)
	if err != nil {
		return nil, err
	}
	graphOut, err := runGo(ctx, dir, "mod", "graph")
	if err != nil {
		return nil, fmt.Errorf("go dependency graph: %w", err)
	}
	dependencies := parseGoGraph(graphOut, byToken)
	return finish(root, modules, dependencies, "go-list", "GO")
}

func runGo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func parseGoModules(data []byte) ([]*agentv0.Component, *agentv0.Component, map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var components []*agentv0.Component
	var root *agentv0.Component
	byToken := map[string]string{}
	for decoder.More() {
		var module goModule
		if err := decoder.Decode(&module); err != nil {
			return nil, nil, nil, fmt.Errorf("decode go module graph: %w", err)
		}
		resolved := module
		if module.Replace != nil {
			resolved.Version = module.Replace.Version
		}
		componentType := agentv0.ComponentType_LIBRARY
		if module.Main {
			componentType = agentv0.ComponentType_MODULE
		}
		group, name := splitName(module.Path)
		purl := "pkg:golang/" + module.Path
		if resolved.Version != "" {
			purl += "@" + resolved.Version
		}
		component := &agentv0.Component{
			Name:    name,
			Group:   group,
			Version: resolved.Version,
			Type:    componentType,
			Purl:    purl,
			BomRef:  purl,
		}
		token := module.Path
		if module.Version != "" {
			token += "@" + module.Version
		}
		byToken[token] = component.BomRef
		if module.Main {
			root = component
		} else {
			components = append(components, component)
		}
	}
	if root == nil {
		return nil, nil, nil, fmt.Errorf("go list returned no main module")
	}
	return components, root, byToken, nil
}

func parseGoGraph(data []byte, byToken map[string]string) []*agentv0.Dependency {
	edges := map[string][]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		from, fromOK := byToken[fields[0]]
		to, toOK := byToken[fields[1]]
		if fromOK && toOK {
			edges[from] = append(edges[from], to)
		}
	}
	refs := make([]string, 0, len(byToken))
	seen := map[string]struct{}{}
	for _, ref := range byToken {
		if _, ok := seen[ref]; !ok {
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	dependencies := make([]*agentv0.Dependency, 0, len(refs))
	for _, ref := range refs {
		dependencies = append(dependencies, &agentv0.Dependency{Ref: ref, DependsOn: edges[ref]})
	}
	return dependencies
}
