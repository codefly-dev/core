package testing

import (
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
)

const (
	deploymentTemplateRoot = "templates/deployment"
	baseTemplateDir        = "kustomize/base"
	overlayTemplateDir     = "kustomize/overlays/environment"
)

// AssertNoDeadDeploymentTemplates fails if any embedded deployment `.tmpl` file
// is dead: it sits outside the rendered kustomize pipeline entirely (e.g. an
// orphaned root serviceaccount.yaml.tmpl that no kustomization references), or
// its rendered filename is referenced by no kustomization. A dead template
// drifts silently — it looks maintained but never ships.
//
// This is an opt-in helper agents call alongside AssertKustomizeTemplates, not
// a step folded into it: the analysis is static (it scans the raw kustomization
// template sources, it does not render), so it must not be gated on any
// particular parameter set.
func AssertNoDeadDeploymentTemplates(t *testing.T, templates fs.FS) {
	t.Helper()
	dead, err := deadDeploymentTemplates(templates)
	if err != nil {
		t.Fatalf("scan deployment templates for dead files: %v", err)
	}
	if len(dead) > 0 {
		t.Fatalf("dead/unreferenced deployment templates (no kustomization references their rendered output):\n  %s",
			strings.Join(dead, "\n  "))
	}
}

// deadDeploymentTemplates returns the deployment templates no kustomization
// reaches. It works on the template sources rather than a rendered tree so that
// a manifest gated behind a conditional (rendered empty under some parameter
// sets) is not mistaken for dead, and so that a file referenced from any
// resource-bearing kustomization field — resources, bases, components, patches,
// patchesStrategicMerge, crds, transformers, generators, *Generator files — is
// seen as referenced. Both arms of a template conditional live in the source,
// so the scan is branch- and parameter-insensitive by construction.
func deadDeploymentTemplates(templates fs.FS) ([]string, error) {
	var kustomizationSources []string
	type manifestTemplate struct {
		templatePath string
		dir          string
		name         string
	}
	var manifests []manifestTemplate

	err := fs.WalkDir(templates, deploymentTemplateRoot, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(p, ".tmpl") {
			return nil
		}
		name := strings.TrimSuffix(path.Base(p), ".tmpl")
		if name == "kustomization.yaml" || name == "kustomization.yml" {
			content, readErr := fs.ReadFile(templates, p)
			if readErr != nil {
				return readErr
			}
			kustomizationSources = append(kustomizationSources, string(content))
			return nil
		}
		manifests = append(manifests, manifestTemplate{templatePath: p, dir: path.Dir(p), name: name})
		return nil
	})
	if err != nil {
		return nil, err
	}

	var dead []string
	for _, manifest := range manifests {
		rel := strings.TrimPrefix(manifest.dir, deploymentTemplateRoot+"/")
		if rel != baseTemplateDir && rel != overlayTemplateDir {
			// Not one of the two rendered kustomize roots — never rendered.
			dead = append(dead, manifest.templatePath)
			continue
		}
		if !referencedByKustomization(kustomizationSources, manifest.name) {
			dead = append(dead, manifest.templatePath)
		}
	}
	sort.Strings(dead)
	return dead, nil
}

// referencedByKustomization reports whether a rendered file name appears as a
// file reference in any kustomization source.
func referencedByKustomization(sources []string, name string) bool {
	for _, source := range sources {
		for _, line := range strings.Split(source, "\n") {
			if lineReferencesFile(line, name) {
				return true
			}
		}
	}
	return false
}

// lineReferencesFile reports whether a single kustomization line names a file
// whose base name matches name. It handles YAML sequence items (`- x.yaml`),
// mapping values (`path: x.yaml`), and the combination of the two
// (`- path: x.yaml`), tolerating relative paths (`../../base/x.yaml`) and
// quoting.
func lineReferencesFile(line, name string) bool {
	value := strings.TrimSpace(line)
	value = strings.TrimSpace(strings.TrimPrefix(value, "- "))
	if index := strings.Index(value, ": "); index >= 0 {
		value = strings.TrimSpace(value[index+2:])
	}
	value = strings.Trim(value, `"'`)
	if value == "" {
		return false
	}
	return path.Base(value) == name
}
