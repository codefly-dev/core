package sbom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type goModule struct {
	Path    string    `json:"Path"`
	Version string    `json:"Version"`
	Main    bool      `json:"Main"`
	Dir     string    `json:"Dir"`
	Replace *goModule `json:"Replace,omitempty"`
}

// GolangOptions selects the same dependency context used by the owning plugin.
type GolangOptions struct {
	// UseWorkspace keeps the active go.work graph. It must be true when the
	// package/build operation also used that Codefly-managed workspace.
	UseWorkspace bool
}

// Golang inventories the exact isolated module graph selected by go.mod/go.sum
// without mutating either file. Ambient go.work files are disabled by default.
func Golang(ctx context.Context, dir string) (*Result, error) {
	return GolangWithOptions(ctx, dir, GolangOptions{})
}

// GolangWithOptions inventories the module graph in the dependency context
// explicitly selected by the language plugin.
func GolangWithOptions(ctx context.Context, dir string, options GolangOptions) (*Result, error) {
	goMod, err := runGo(ctx, dir, options, "env", "GOMOD")
	if err != nil {
		return nil, fmt.Errorf("go source module: %w", err)
	}
	sourceGoMod := strings.TrimSpace(string(goMod))
	if sourceGoMod == "" || sourceGoMod == os.DevNull {
		return nil, fmt.Errorf("no Go source module contains %q", dir)
	}
	modulesOut, err := runGo(ctx, dir, options, "list", "-mod=readonly", "-m", "-json", "all")
	if err != nil {
		return nil, fmt.Errorf("go module inventory: %w", err)
	}
	// GOMOD locates the module root even when -modfile selects a different
	// manifest. Compare directories, not the selected manifest filenames.
	modules, root, byToken, err := parseGoModules(modulesOut, filepath.Dir(sourceGoMod))
	if err != nil {
		return nil, err
	}
	graphOut, err := runGo(ctx, dir, options, "mod", "graph")
	if err != nil {
		return nil, fmt.Errorf("go dependency graph: %w", err)
	}
	dependencies := parseGoGraph(graphOut, byToken)
	return finish(root, modules, dependencies, "go-list", "GO")
}

func runGo(ctx context.Context, dir string, options GolangOptions, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if !options.UseWorkspace {
		cmd.Env = append(cmd.Env, "GOWORK=off")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func parseGoModules(data []byte, sourceDir string) ([]*agentv0.Component, *agentv0.Component, map[string]string, error) {
	source, err := os.Stat(sourceDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stat Go source module: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var components []*agentv0.Component
	var root *agentv0.Component
	byToken := map[string]string{}
	for decoder.More() {
		var module goModule
		if err := decoder.Decode(&module); err != nil {
			return nil, nil, nil, fmt.Errorf("decode go module graph: %w", err)
		}
		resolvedVersion := module.Version
		if module.Replace != nil && module.Replace.Version != "" {
			resolvedVersion = module.Replace.Version
		}
		componentType := agentv0.ComponentType_LIBRARY
		if module.Main {
			componentType = agentv0.ComponentType_MODULE
		}
		group, name := splitName(module.Path)
		purl := "pkg:golang/" + module.Path
		if resolvedVersion != "" {
			purl += "@" + resolvedVersion
		}
		component := &agentv0.Component{
			Name:    name,
			Group:   group,
			Version: resolvedVersion,
			Type:    componentType,
			Purl:    purl,
			BomRef:  purl,
		}
		token := module.Path
		if module.Version != "" {
			token += "@" + module.Version
		}
		byToken[token] = component.BomRef
		isRoot := false
		if module.Main {
			info, err := os.Stat(module.Dir)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("stat main module %q: %w", module.Path, err)
			}
			isRoot = os.SameFile(source, info)
		}
		if isRoot {
			if root != nil {
				return nil, nil, nil, fmt.Errorf("multiple main modules match source directory %q", sourceDir)
			}
			root = component
		} else {
			components = append(components, component)
		}
	}
	if root == nil {
		return nil, nil, nil, fmt.Errorf("no main module matches source directory %q", sourceDir)
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
		if !toOK {
			// Requirements can name a version of a workspace main module.
			// Only targets map to that selected module: outgoing edges of an
			// unselected released version do not describe the local module.
			path, _, versioned := strings.Cut(fields[1], "@")
			if versioned {
				to, toOK = byToken[path]
			}
		}
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
