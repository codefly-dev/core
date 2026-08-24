package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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

// podTemplateWorkloadKinds are the Kubernetes kinds whose pod template lives at
// spec.template — every first-party service agent renders one of these
// (Deployment for stateless services, StatefulSet for postgres/redis). Binding
// the pod identity here, rather than in each agent's template, is what lets all
// agents inherit the overlay from core.
var podTemplateWorkloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"ReplicaSet":  true,
	"Job":         true,
}

// applyPodOverlay binds the pod-template overlay into the rendered base
// manifests: it stamps serviceAccountName onto each workload's pod spec and
// merges the pod labels and annotations. Doing this centrally means an agent
// only has to pass the overlay parameter — it never re-implements the binding
// in its own template. The write is idempotent: a field the template already
// rendered is left untouched, so an agent mid-migration that still carries the
// binding does not double-render it.
func applyPodOverlay(_ context.Context, baseDir string, overlay *PodTemplateOverlay) error {
	if overlay == nil {
		return nil
	}
	if !overlay.HasServiceAccount() && len(overlay.PodLabels) == 0 && len(overlay.PodAnnotations) == 0 {
		return nil
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return fmt.Errorf("read base dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		if err = applyPodOverlayToFile(filepath.Join(baseDir, entry.Name()), overlay); err != nil {
			return err
		}
	}
	return nil
}

func applyPodOverlayToFile(path string, overlay *PodTemplateOverlay) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var documents []*yaml.Node
	changed := false
	for {
		var document yaml.Node
		err = decoder.Decode(&document)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("parse manifest %s: %w", path, err)
		}
		if len(document.Content) == 0 {
			continue
		}
		if applyPodOverlayToDocument(document.Content[0], overlay) {
			changed = true
		}
		documents = append(documents, &document)
	}
	if !changed {
		return nil
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	for _, document := range documents {
		if err = encoder.Encode(document); err != nil {
			return fmt.Errorf("render manifest %s: %w", path, err)
		}
	}
	if err = encoder.Close(); err != nil {
		return fmt.Errorf("render manifest %s: %w", path, err)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func applyPodOverlayToDocument(root *yaml.Node, overlay *PodTemplateOverlay) bool {
	if root == nil || root.Kind != yaml.MappingNode {
		return false
	}
	if !podTemplateWorkloadKinds[mappingScalar(root, "kind")] {
		return false
	}
	template := mappingChild(mappingChild(root, "spec"), "template")
	if template == nil {
		return false
	}
	changed := false
	if len(overlay.PodLabels) > 0 {
		metadata := ensureMappingChild(template, "metadata")
		labels := ensureMappingChild(metadata, "labels")
		if mergeScalarsIfAbsent(labels, overlay.PodLabels) {
			changed = true
		}
	}
	if len(overlay.PodAnnotations) > 0 {
		metadata := ensureMappingChild(template, "metadata")
		annotations := ensureMappingChild(metadata, "annotations")
		if mergeScalarsIfAbsent(annotations, overlay.PodAnnotations) {
			changed = true
		}
	}
	if overlay.HasServiceAccount() {
		podSpec := ensureMappingChild(template, "spec")
		if setScalarIfAbsent(podSpec, "serviceAccountName", overlay.ServiceAccountName(), false) {
			changed = true
		}
	}
	return changed
}

// mappingChild returns the value node for key in a mapping node, or nil.
func mappingChild(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func mappingScalar(node *yaml.Node, key string) string {
	if child := mappingChild(node, key); child != nil {
		return child.Value
	}
	return ""
}

// ensureMappingChild returns the mapping node at key, creating an empty one when
// absent so nested fields can be stamped in.
func ensureMappingChild(node *yaml.Node, key string) *yaml.Node {
	if child := mappingChild(node, key); child != nil {
		return child
	}
	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
	return value
}

// setScalarIfAbsent adds a scalar key only when the mapping does not already
// carry it, so a value the template rendered wins. It reports whether it wrote.
func setScalarIfAbsent(node *yaml.Node, key, value string, quoted bool) bool {
	if mappingChild(node, key) != nil {
		return false
	}
	valueNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	if quoted {
		valueNode.Style = yaml.DoubleQuotedStyle
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		valueNode)
	return true
}

// mergeScalarsIfAbsent stamps quoted string values into a mapping in a stable
// key order, leaving any key the mapping already carries untouched.
func mergeScalarsIfAbsent(node *yaml.Node, values map[string]string) bool {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changed := false
	for _, key := range keys {
		if setScalarIfAbsent(node, key, values[key], true) {
			changed = true
		}
	}
	return changed
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
