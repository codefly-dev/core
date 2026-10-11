package services

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

const confinedDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: target
spec:
  template:
    spec:
      containers:
      - name: api
        image: example/api
`

// confinedBase writes a base whose files exist only in memory, so a read or
// write that leaves the supplied filesystem cannot find them.
func confinedBase(t *testing.T, kustomization string, files map[string]string) (filesys.FileSystem, string) {
	t.Helper()
	filesystem := filesys.MakeFsInMemory()
	base := "/render/base"
	require.NoError(t, filesystem.MkdirAll(base))
	require.NoError(t, filesystem.WriteFile(filepath.Join(base, "kustomization.yaml"), []byte(kustomization)))
	require.NoError(t, filesystem.WriteFile(filepath.Join(base, "deployment.yaml"), []byte(confinedDeployment)))
	for name, body := range files {
		require.NoError(t, filesystem.MkdirAll(filepath.Dir(filepath.Join(base, name))))
		require.NoError(t, filesystem.WriteFile(filepath.Join(base, name), []byte(body)))
	}
	return filesystem, base
}

func confinedOverlay(t *testing.T) *PodTemplateOverlay {
	t.Helper()
	overlay := &PodTemplateOverlay{}
	require.NoError(t, overlay.AttachServiceAccount(&WorkloadServiceAccount{Annotations: map[string]string{"example.test/identity": "principal"}}, map[string]string{"example.test/workload": "true"}))
	return overlay
}

// A reference Kustomize would clone or fetch leaves any filesystem: it is
// refused before the build, and nothing is written.
func TestProjectServiceAccountOnFSRefusesRemoteReferences(t *testing.T) {
	for name, kustomization := range map[string]string{
		"file repository":    "resources: [deployment.yaml, 'file:///outside/repo.git']\n",
		"https repository":   "resources: [deployment.yaml, 'https://example.test/org/repo//base?ref=v1']\n",
		"github shorthand":   "resources: [deployment.yaml, 'github.com/org/repo/base']\n",
		"scp repository":     "resources: [deployment.yaml, 'git@example.test:org/repo.git']\n",
		"forced git":         "resources: [deployment.yaml, 'git::example.test/org/repo']\n",
		"remote component":   "resources: [deployment.yaml]\ncomponents: ['https://example.test/c']\n",
		"remote generator":   "resources: [deployment.yaml]\ngenerators: ['github.com/org/repo/gen']\n",
		"remote patch file":  "resources: [deployment.yaml]\npatches:\n- path: https://example.test/patch.yaml\n",
		"remote config file": "resources: [deployment.yaml]\nconfigMapGenerator:\n- name: c\n  files: [k=https://example.test/v]\n",
		"nested remote":      "resources: [deployment.yaml, nested]\n",
	} {
		t.Run(name, func(t *testing.T) {
			filesystem, base := confinedBase(t, kustomization, map[string]string{
				"nested/kustomization.yaml": "resources: ['https://example.test/r.yaml']\n",
			})
			err := ProjectServiceAccountOnFS(context.Background(), filesystem, base, "target", "api", confinedOverlay(t))
			require.ErrorContains(t, err, "remote")
			require.False(t, filesystem.Exists(filepath.Join(base, "serviceaccount.yaml")))
		})
	}
}

// A URL in a field Kustomize never loads is data, and an image digest or a
// registry port is not a repository.
func TestProjectServiceAccountOnFSAcceptsURLsThatAreData(t *testing.T) {
	filesystem, base := confinedBase(t, `resources: [deployment.yaml]
commonAnnotations:
  example.test/docs: https://example.test/docs
images:
- name: example/api
  newName: registry.example.test:5000/api
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
configMapGenerator:
- name: c
  literals: [URL=https://example.test/v]
`, nil)
	require.NoError(t, ProjectServiceAccountOnFS(context.Background(), filesystem, base, "target", "api", confinedOverlay(t)))
}

// An account that exists only in the supplied filesystem is the one merged
// and checked: a read that went to the disk would miss it.
func TestProjectServiceAccountOnFSReadsTheExistingAccountFromTheSuppliedFilesystem(t *testing.T) {
	existing := "apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: api\n  namespace: target\n  annotations:\n    example.test/kept: \"yes\"\n"
	filesystem, base := confinedBase(t, "resources: [deployment.yaml, serviceaccount.yaml]\n", map[string]string{"serviceaccount.yaml": existing})
	require.NoError(t, ProjectServiceAccountOnFS(context.Background(), filesystem, base, "target", "api", confinedOverlay(t)))
	account, err := filesystem.ReadFile(filepath.Join(base, "serviceaccount.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(account), "example.test/kept")
	require.Contains(t, string(account), "example.test/identity: principal")

	conflicting := strings.Replace(existing, "name: api", "name: other", 1)
	filesystem, base = confinedBase(t, "resources: [deployment.yaml, serviceaccount.yaml]\n", map[string]string{"serviceaccount.yaml": conflicting})
	require.ErrorContains(t, ProjectServiceAccountOnFS(context.Background(), filesystem, base, "target", "api", confinedOverlay(t)), "conflicts")
}

func TestProjectServiceAccountOnFSUsesOnlySuppliedFilesystem(t *testing.T) {
	filesystem := filesys.MakeFsInMemory()
	base := "/render/base"
	require.NoError(t, filesystem.MkdirAll(base))
	require.NoError(t, filesystem.WriteFile(filepath.Join(base, "kustomization.yaml"), []byte("resources: [deployment.yaml]\n")))
	require.NoError(t, filesystem.WriteFile(filepath.Join(base, "deployment.yaml"), []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: target
spec:
  template:
    spec:
      containers:
      - name: api
        image: example/api
`)))
	overlay := &PodTemplateOverlay{}
	require.NoError(t, overlay.AttachServiceAccount(&WorkloadServiceAccount{Annotations: map[string]string{"example.test/identity": "principal"}}, map[string]string{"example.test/workload": "true"}))
	require.NoError(t, ProjectServiceAccountOnFS(context.Background(), filesystem, base, "target", "api", overlay))
	account, err := filesystem.ReadFile(filepath.Join(base, "serviceaccount.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(account), "example.test/identity: principal")
	deployment, err := filesystem.ReadFile(filepath.Join(base, "deployment.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(deployment), "serviceAccountName: api")
	require.Contains(t, string(deployment), `example.test/workload: "true"`)
	index, err := filesystem.ReadFile(filepath.Join(base, "kustomization.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(index), "serviceaccount.yaml")
	require.NotContains(t, string(index), "buildMetadata")
}
