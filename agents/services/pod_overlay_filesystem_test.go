package services

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

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
