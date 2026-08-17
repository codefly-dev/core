package testing

import (
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

func baseTemplates() fstest.MapFS {
	return fstest.MapFS{
		"templates/deployment/kustomize/base/kustomization.yaml.tmpl":                 {Data: []byte(baseKustomization)},
		"templates/deployment/kustomize/base/deployment.yaml.tmpl":                    {Data: []byte("kind: Deployment\n")},
		"templates/deployment/kustomize/overlays/environment/kustomization.yaml.tmpl": {Data: []byte(overlayKustomization)},
	}
}

func TestDeadDeploymentTemplatesAcceptsFullyWiredTree(t *testing.T) {
	dead, err := deadDeploymentTemplates(baseTemplates())
	require.NoError(t, err)
	require.Empty(t, dead)
}

func TestDeadDeploymentTemplatesFlagsOrphanOutsidePipeline(t *testing.T) {
	templates := baseTemplates()
	// A serviceaccount template that sits outside the rendered kustomize roots
	// — the concrete orphan case the plugin issue describes.
	templates["templates/deployment/serviceaccount.yaml.tmpl"] = &fstest.MapFile{Data: []byte("kind: ServiceAccount\n")}
	dead, err := deadDeploymentTemplates(templates)
	require.NoError(t, err)
	require.Equal(t, []string{"templates/deployment/serviceaccount.yaml.tmpl"}, dead)
}

func TestDeadDeploymentTemplatesFlagsUnreferencedBaseTemplate(t *testing.T) {
	templates := baseTemplates()
	// Rendered into base/ but no kustomization lists it.
	templates["templates/deployment/kustomize/base/serviceaccount.yaml.tmpl"] = &fstest.MapFile{Data: []byte("kind: ServiceAccount\n")}
	dead, err := deadDeploymentTemplates(templates)
	require.NoError(t, err)
	require.Equal(t, []string{"templates/deployment/kustomize/base/serviceaccount.yaml.tmpl"}, dead)
}

// TestDeadDeploymentTemplatesAcceptsPatchReference guards the regression where
// a file referenced only through a resource-bearing field other than resources/
// bases (here patchesStrategicMerge) was mistaken for dead.
func TestDeadDeploymentTemplatesAcceptsPatchReference(t *testing.T) {
	templates := baseTemplates()
	templates["templates/deployment/kustomize/base/kustomization.yaml.tmpl"] = &fstest.MapFile{Data: []byte(
		baseKustomization + "patchesStrategicMerge:\n  - service-patch.yaml\ncomponents:\n  - ../component\n")}
	templates["templates/deployment/kustomize/base/service-patch.yaml.tmpl"] = &fstest.MapFile{Data: []byte("kind: Deployment\n")}
	dead, err := deadDeploymentTemplates(templates)
	require.NoError(t, err)
	require.Empty(t, dead)
}

// TestDeadDeploymentTemplatesAcceptsConditionalReference guards the regression
// where a manifest listed only inside a template conditional (so it renders
// empty under some parameter sets) was mistaken for dead.
func TestDeadDeploymentTemplatesAcceptsConditionalReference(t *testing.T) {
	templates := baseTemplates()
	templates["templates/deployment/kustomize/base/kustomization.yaml.tmpl"] = &fstest.MapFile{Data: []byte(
		baseKustomization + "{{- if not .Restricted }}\n  - secret.yaml\n{{- end }}\n")}
	templates["templates/deployment/kustomize/base/secret.yaml.tmpl"] = &fstest.MapFile{Data: []byte("kind: Secret\n")}
	dead, err := deadDeploymentTemplates(templates)
	require.NoError(t, err)
	require.Empty(t, dead)
}

// TestDeadDeploymentTemplatesAcceptsPathReference covers a file pulled in via a
// patches `path:` field with a relative prefix.
func TestDeadDeploymentTemplatesAcceptsPathReference(t *testing.T) {
	templates := baseTemplates()
	templates["templates/deployment/kustomize/base/kustomization.yaml.tmpl"] = &fstest.MapFile{Data: []byte(
		baseKustomization + "patches:\n  - path: ../base/hpa.yaml\n    target:\n      kind: Deployment\n")}
	templates["templates/deployment/kustomize/base/hpa.yaml.tmpl"] = &fstest.MapFile{Data: []byte("kind: HorizontalPodAutoscaler\n")}
	dead, err := deadDeploymentTemplates(templates)
	require.NoError(t, err)
	require.Empty(t, dead)
}
