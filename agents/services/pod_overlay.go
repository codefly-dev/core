package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// PodTemplateOverlay is the shared, typed model for the pod/workload
// customizations common to every Codefly service agent. It is carried through
// DeployKustomize as a typed field so agents render the same contract instead
// of stuffing a plugin-local struct into an `any` and blind-accessing its
// fields from templates.
type PodTemplateOverlay struct {
	// ServiceAccount, when set, renders a codefly-owned ServiceAccount object
	// and binds the workload's pods to it via serviceAccountName. Left nil, the
	// pods keep running under the namespace default.
	ServiceAccount *WorkloadServiceAccount

	// PodLabels are stamped onto the pod template metadata, e.g. the
	// azure.workload.identity/use: "true" label the identity webhook keys off.
	PodLabels map[string]string

	// PodAnnotations are stamped onto the pod template metadata.
	PodAnnotations map[string]string
}

// WorkloadServiceAccount describes the Kubernetes ServiceAccount a workload's
// pods run under. Annotations land on the rendered SA object — this is the
// passwordless-identity seam, carrying e.g. an Azure workload-identity client
// id so the identity webhook can federate a token.
type WorkloadServiceAccount struct {
	Name        string
	Annotations map[string]string
}

// dns1123Subdomain matches a Kubernetes ServiceAccount name.
var dns1123Subdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// Validate rejects an overlay that would silently half-apply. A ServiceAccount
// with annotations but no name would render nothing and leave the pod on the
// default SA — token minting then has no identity and connections fail with no
// deploy error — so a present ServiceAccount must carry a valid name.
func (o *PodTemplateOverlay) Validate() error {
	if o == nil || o.ServiceAccount == nil {
		return nil
	}
	name := o.ServiceAccount.Name
	if name == "" {
		return fmt.Errorf("workload service account requires a name")
	}
	if len(name) > 253 || !dns1123Subdomain.MatchString(name) {
		return fmt.Errorf("workload service account name %q must be a DNS-1123 subdomain", name)
	}
	return nil
}

// HasServiceAccount reports whether a ServiceAccount object should render and
// the pod should bind serviceAccountName.
func (o *PodTemplateOverlay) HasServiceAccount() bool {
	return o != nil && o.ServiceAccount != nil && o.ServiceAccount.Name != ""
}

// ServiceAccountName returns the name the pod binds to, or "" when unset.
func (o *PodTemplateOverlay) ServiceAccountName() string {
	if !o.HasServiceAccount() {
		return ""
	}
	return o.ServiceAccount.Name
}

// emitWorkloadServiceAccount renders the codefly-owned ServiceAccount object
// into the kustomize base and wires it into the base kustomization. The
// app.kubernetes.io/managed-by: codefly ownership label is baked in here so the
// SA is conformant by construction — no agent re-implements the label and none
// can forget it (ValidateKubernetesManifestTree requires it on ServiceAccounts).
func emitWorkloadServiceAccount(_ context.Context, baseDir, namespace string, sa *WorkloadServiceAccount) error {
	metadata := map[string]any{
		"name":      sa.Name,
		"namespace": namespace,
		"labels":    map[string]any{"app.kubernetes.io/managed-by": "codefly"},
	}
	if len(sa.Annotations) > 0 {
		annotations := make(map[string]any, len(sa.Annotations))
		for key, value := range sa.Annotations {
			annotations[key] = value
		}
		metadata["annotations"] = annotations
	}
	document, err := yaml.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata":   metadata,
	})
	if err != nil {
		return fmt.Errorf("marshal service account: %w", err)
	}
	if err = os.WriteFile(filepath.Join(baseDir, "serviceaccount.yaml"), document, 0o644); err != nil {
		return fmt.Errorf("write service account: %w", err)
	}
	return addKustomizeResource(filepath.Join(baseDir, "kustomization.yaml"), "serviceaccount.yaml")
}

// addKustomizeResource appends a resource to a kustomization's resources list,
// idempotently. It round-trips the rendered YAML rather than editing the
// plugin template so the wiring stays owned by core.
func addKustomizeResource(kustomizationPath, resource string) error {
	content, err := os.ReadFile(kustomizationPath)
	if err != nil {
		return fmt.Errorf("read kustomization: %w", err)
	}
	var document map[string]any
	if err = yaml.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("parse kustomization: %w", err)
	}
	if document == nil {
		document = map[string]any{}
	}
	resources, _ := document["resources"].([]any)
	for _, existing := range resources {
		if name, ok := existing.(string); ok && name == resource {
			return nil
		}
	}
	document["resources"] = append(resources, resource)
	out, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal kustomization: %w", err)
	}
	return os.WriteFile(kustomizationPath, out, 0o644)
}
