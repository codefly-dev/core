package testing

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

const baseKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
`

const overlayKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
`

// renderTree writes a rendered kustomize tree (relative path -> content) under
// a fresh temp dir and returns it.
func renderTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return dir
}

func TestDeadDeploymentTemplatesAcceptsFullyWiredTree(t *testing.T) {
	templates := fstest.MapFS{
		"templates/deployment/kustomize/base/kustomization.yaml.tmpl":                 {Data: []byte(baseKustomization)},
		"templates/deployment/kustomize/base/deployment.yaml.tmpl":                    {Data: []byte("kind: Deployment\n")},
		"templates/deployment/kustomize/overlays/environment/kustomization.yaml.tmpl": {Data: []byte(overlayKustomization)},
	}
	rendered := renderTree(t, map[string]string{
		"base/kustomization.yaml":          baseKustomization,
		"base/deployment.yaml":             "kind: Deployment\n",
		"overlays/test/kustomization.yaml": overlayKustomization,
	})
	dead, err := deadDeploymentTemplates(templates, rendered)
	require.NoError(t, err)
	require.Empty(t, dead)
}

func TestDeadDeploymentTemplatesFlagsOrphanOutsidePipeline(t *testing.T) {
	templates := fstest.MapFS{
		"templates/deployment/kustomize/base/kustomization.yaml.tmpl":                 {Data: []byte(baseKustomization)},
		"templates/deployment/kustomize/base/deployment.yaml.tmpl":                    {Data: []byte("kind: Deployment\n")},
		"templates/deployment/kustomize/overlays/environment/kustomization.yaml.tmpl": {Data: []byte(overlayKustomization)},
		// A serviceaccount template that sits outside the rendered kustomize
		// roots — the concrete orphan case the plugin issue describes.
		"templates/deployment/serviceaccount.yaml.tmpl": {Data: []byte("kind: ServiceAccount\n")},
	}
	rendered := renderTree(t, map[string]string{
		"base/kustomization.yaml":          baseKustomization,
		"base/deployment.yaml":             "kind: Deployment\n",
		"overlays/test/kustomization.yaml": overlayKustomization,
	})
	dead, err := deadDeploymentTemplates(templates, rendered)
	require.NoError(t, err)
	require.Equal(t, []string{"templates/deployment/serviceaccount.yaml.tmpl"}, dead)
}

func TestDeadDeploymentTemplatesFlagsUnreferencedBaseTemplate(t *testing.T) {
	templates := fstest.MapFS{
		"templates/deployment/kustomize/base/kustomization.yaml.tmpl": {Data: []byte(baseKustomization)},
		"templates/deployment/kustomize/base/deployment.yaml.tmpl":    {Data: []byte("kind: Deployment\n")},
		// Rendered into base/ but no kustomization lists it.
		"templates/deployment/kustomize/base/serviceaccount.yaml.tmpl":                {Data: []byte("kind: ServiceAccount\n")},
		"templates/deployment/kustomize/overlays/environment/kustomization.yaml.tmpl": {Data: []byte(overlayKustomization)},
	}
	rendered := renderTree(t, map[string]string{
		"base/kustomization.yaml":          baseKustomization,
		"base/deployment.yaml":             "kind: Deployment\n",
		"base/serviceaccount.yaml":         "kind: ServiceAccount\n",
		"overlays/test/kustomization.yaml": overlayKustomization,
	})
	dead, err := deadDeploymentTemplates(templates, rendered)
	require.NoError(t, err)
	require.Equal(t, []string{"templates/deployment/kustomize/base/serviceaccount.yaml.tmpl"}, dead)
}
