package testing

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// AssertNoDeadDeploymentTemplates renders a plugin's deployment templates and
// fails if any embedded `.tmpl` file is dead: either it sits outside the
// rendered kustomize pipeline entirely (e.g. an orphaned root
// serviceaccount.yaml.tmpl that no kustomization references), or its rendered
// output is unreachable from the environment overlay. A dead template drifts
// silently — it looks maintained but never ships — so the contract forbids it.
func AssertNoDeadDeploymentTemplates(t *testing.T, templates fs.FS, parameters any) {
	t.Helper()
	renderedDir := assertKustomizeProfile(
		t,
		templates,
		parameters,
		ephemeralProfile,
	)
	dead, err := deadDeploymentTemplates(templates, renderedDir)
	if err != nil {
		t.Fatalf("scan deployment templates for dead files: %v", err)
	}
	if len(dead) > 0 {
		t.Fatalf("dead/unreferenced deployment templates (no kustomization reaches their rendered output):\n  %s",
			strings.Join(dead, "\n  "))
	}
}

// deadDeploymentTemplates returns the deployment templates whose rendered
// output no kustomization reaches, resolved against an already-rendered tree.
func deadDeploymentTemplates(templates fs.FS, renderedDir string) ([]string, error) {
	env, err := overlayEnvironment(renderedDir)
	if err != nil {
		return nil, err
	}
	reachable, err := reachableManifestFiles(renderedDir, env)
	if err != nil {
		return nil, err
	}

	var dead []string
	err = fs.WalkDir(templates, "templates/deployment", func(templatePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(templatePath, ".tmpl") {
			return nil
		}
		rendered, inPipeline := renderedManifestPath(templatePath, env)
		if !inPipeline {
			dead = append(dead, templatePath)
			return nil
		}
		if _, ok := reachable[filepath.Join(renderedDir, rendered)]; !ok {
			dead = append(dead, templatePath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dead)
	return dead, nil
}

// renderedManifestPath maps a template path in the embedded FS to the path its
// output lands at in the rendered tree. Templates outside the two rendered
// kustomize roots are not part of the pipeline at all.
func renderedManifestPath(templatePath, env string) (string, bool) {
	rel := strings.TrimPrefix(templatePath, "templates/deployment/")
	rel = strings.TrimSuffix(rel, ".tmpl")
	if sub, ok := strings.CutPrefix(rel, "kustomize/base/"); ok {
		return filepath.Join("base", sub), true
	}
	if sub, ok := strings.CutPrefix(rel, "kustomize/overlays/environment/"); ok {
		return filepath.Join("overlays", env, sub), true
	}
	return "", false
}

func overlayEnvironment(renderedDir string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(renderedDir, "overlays"))
	if err != nil {
		return "", fmt.Errorf("read overlays: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return entry.Name(), nil
		}
	}
	return "", fmt.Errorf("no environment overlay rendered under %s", renderedDir)
}

// reachableManifestFiles walks the kustomization graph starting from the
// environment overlay and returns the set of absolute file paths it reaches,
// including every visited kustomization file.
func reachableManifestFiles(renderedDir, env string) (map[string]struct{}, error) {
	reachable := make(map[string]struct{})
	queue := []string{filepath.Join(renderedDir, "overlays", env)}
	visited := make(map[string]struct{})
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if _, seen := visited[dir]; seen {
			continue
		}
		visited[dir] = struct{}{}
		kustomization, err := kustomizationPath(dir)
		if err != nil {
			return nil, err
		}
		if kustomization == "" {
			continue
		}
		reachable[kustomization] = struct{}{}
		resources, err := kustomizationResources(kustomization)
		if err != nil {
			return nil, err
		}
		for _, resource := range resources {
			target := filepath.Clean(filepath.Join(dir, resource))
			info, err := os.Stat(target)
			if err != nil {
				// A resource that does not resolve is a broken kustomization,
				// not a dead template; the manifest contract check surfaces it.
				continue
			}
			if info.IsDir() {
				queue = append(queue, target)
				continue
			}
			reachable[target] = struct{}{}
		}
	}
	return reachable, nil
}

func kustomizationPath(dir string) (string, error) {
	for _, name := range []string{"kustomization.yaml", "kustomization.yml"} {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

func kustomizationResources(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document struct {
		Resources []string `yaml:"resources"`
		Bases     []string `yaml:"bases"`
	}
	if err = yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return append(document.Resources, document.Bases...), nil
}
