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

	// ConfigMounts mount ConfigMaps as files into the workload's pods. This is
	// the file-based config seam — a ConfigMap rendered onto disk at a container
	// path — as opposed to envFrom, which only projects keys as environment
	// variables. Left nil/empty, no volumes or volumeMounts render.
	ConfigMounts []ConfigMount

	// Containers are extra containers merged into the workload's pod, appended to
	// the primary container the template renders. This is the cross-service
	// placement seam: a dependency declared with Placement "sidecar" contributes
	// its own rendered container spec here, so it runs in the consumer's pod and
	// is reached over localhost. Each is a raw container spec (the same shape a
	// Deployment's containers[] entry has) and MUST carry a "name"; a container
	// whose name already exists on the pod is left untouched (idempotent).
	Containers []map[string]any
}

// HasContainers reports whether the overlay contributes any extra containers.
func (o *PodTemplateOverlay) HasContainers() bool {
	return o != nil && len(o.Containers) > 0
}

// ConfigMount mounts a ConfigMap as a file (or directory of files) into a
// workload's pods at an absolute container path. The ConfigMap itself may be
// supplied out-of-band per environment — the mount only names it, so the
// workload starts once the ConfigMap exists (or immediately when Optional).
type ConfigMount struct {
	// ConfigMapName is the ConfigMap to mount. Must be a DNS-1123 subdomain.
	ConfigMapName string
	// MountPath is the absolute container path the ConfigMap is mounted at.
	MountPath string
	// ReadOnly mounts the volume read-only. Nil defaults to true in
	// DefaultConfigMounts — read-only is the sensible default for file config —
	// while an explicit true/false is honored.
	ReadOnly *bool
	// Optional lets the pod start even when the ConfigMap is absent, instead of
	// blocking on it. Defaults to false.
	Optional bool
	// VolumeName names the pod volume backing this mount. Derived from
	// ConfigMapName when empty; a caller-supplied name is left untouched.
	VolumeName string
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

// dns1123Label matches a Kubernetes volume name (no dots, unlike a subdomain).
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Validate rejects an overlay that would silently half-apply. A ServiceAccount
// with annotations but no name would render nothing and leave the pod on the
// default SA — token minting then has no identity and connections fail with no
// deploy error — so a present ServiceAccount must carry a valid name.
func (o *PodTemplateOverlay) Validate() error {
	if o == nil {
		return nil
	}
	if o.ServiceAccount != nil {
		name := o.ServiceAccount.Name
		if name == "" {
			return fmt.Errorf("workload service account requires a name")
		}
		if len(name) > 253 || !dns1123Subdomain.MatchString(name) {
			return fmt.Errorf("workload service account name %q must be a DNS-1123 subdomain", name)
		}
	}
	if err := o.validateContainers(); err != nil {
		return err
	}
	return o.validateConfigMounts()
}

// validateContainers rejects a contributed container that could not be merged
// idempotently: one without a name (there is nothing to dedupe on, so a second
// render would duplicate it), a name that is not a DNS-1123 label, or two
// contributions competing for the same name.
func (o *PodTemplateOverlay) validateContainers() error {
	seen := make(map[string]bool, len(o.Containers))
	for _, container := range o.Containers {
		name, _ := container["name"].(string)
		if name == "" {
			return fmt.Errorf("contributed container requires a name")
		}
		if len(name) > 63 || !dns1123Label.MatchString(name) {
			return fmt.Errorf("contributed container name %q must be a DNS-1123 label", name)
		}
		if seen[name] {
			return fmt.Errorf("contributed container name %q appears more than once", name)
		}
		seen[name] = true
	}
	return nil
}

// validateConfigMounts rejects a mount that would render an invalid or
// ambiguous manifest: a relative container path, a ConfigMap name that is not a
// DNS-1123 subdomain, a volume name that is not a DNS-1123 label, two mounts
// competing for the same container path (the second would silently shadow the
// first), or two mounts sharing a volume name (Kubernetes rejects duplicate
// pod volume names — DefaultConfigMounts keeps derived names unique, but a
// caller-supplied VolumeName can still collide).
func (o *PodTemplateOverlay) validateConfigMounts() error {
	seenPaths := make(map[string]bool, len(o.ConfigMounts))
	seenVolumes := make(map[string]bool, len(o.ConfigMounts))
	for _, mount := range o.ConfigMounts {
		if !filepath.IsAbs(mount.MountPath) {
			return fmt.Errorf("config mount path %q must be absolute", mount.MountPath)
		}
		if seenPaths[mount.MountPath] {
			return fmt.Errorf("config mount path %q is mounted more than once", mount.MountPath)
		}
		seenPaths[mount.MountPath] = true
		if l := len(mount.ConfigMapName); l == 0 || l > 253 || !dns1123Subdomain.MatchString(mount.ConfigMapName) {
			return fmt.Errorf("config mount ConfigMap name %q must be a DNS-1123 subdomain", mount.ConfigMapName)
		}
		if mount.VolumeName != "" {
			if len(mount.VolumeName) > 63 || !dns1123Label.MatchString(mount.VolumeName) {
				return fmt.Errorf("config mount volume name %q must be a DNS-1123 label", mount.VolumeName)
			}
			if seenVolumes[mount.VolumeName] {
				return fmt.Errorf("config mount volume name %q is used more than once", mount.VolumeName)
			}
			seenVolumes[mount.VolumeName] = true
		}
	}
	return nil
}

// DefaultServiceAccountName keys the workload's ServiceAccount off the service
// name: an overlay that opts into an SA (ServiceAccount set) but leaves the name
// empty resolves to the service's own name. This is what lets a module opt in
// without hardcoding a per-service identity string. A name the caller supplied
// is left untouched.
func (o *PodTemplateOverlay) DefaultServiceAccountName(serviceName string) {
	if o == nil || o.ServiceAccount == nil || o.ServiceAccount.Name != "" {
		return
	}
	o.ServiceAccount.Name = serviceName
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

// HasConfigMounts reports whether any ConfigMap-backed file mounts should
// render. Templates gate `volumes`/`volumeMounts` on this so an overlay without
// mounts leaves rendering unchanged.
func (o *PodTemplateOverlay) HasConfigMounts() bool {
	return o != nil && len(o.ConfigMounts) > 0
}

// DefaultConfigMounts normalizes each ConfigMount before validation and
// rendering: it derives a DNS-1123-label VolumeName from the ConfigMap name
// when the caller left it empty (kept unique across mounts) and defaults
// ReadOnly to true when the caller left it unset. A caller-supplied VolumeName
// or ReadOnly value is left untouched.
func (o *PodTemplateOverlay) DefaultConfigMounts() {
	if o == nil {
		return
	}
	used := make(map[string]bool, len(o.ConfigMounts))
	for i := range o.ConfigMounts {
		mount := &o.ConfigMounts[i]
		if mount.ReadOnly == nil {
			readOnly := true
			mount.ReadOnly = &readOnly
		}
		if mount.VolumeName == "" {
			mount.VolumeName = uniqueVolumeName(deriveVolumeName(mount.ConfigMapName), used)
		}
		used[mount.VolumeName] = true
	}
}

// deriveVolumeName turns a ConfigMap name (a DNS-1123 subdomain, which may carry
// dots) into a DNS-1123 label suitable for a pod volume name.
func deriveVolumeName(configMapName string) string {
	name := strings.ReplaceAll(configMapName, ".", "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// uniqueVolumeName disambiguates two mounts that derive the same volume name
// (e.g. the same ConfigMap mounted at two paths) by suffixing an index, keeping
// the result a valid DNS-1123 label.
func uniqueVolumeName(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("-%d", i)
		candidate := base
		if len(candidate)+len(suffix) > 63 {
			candidate = strings.TrimRight(candidate[:63-len(suffix)], "-")
		}
		candidate += suffix
		if !used[candidate] {
			return candidate
		}
	}
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

// podOverlayResult reports what applyPodOverlay did across the base tree.
type podOverlayResult struct {
	// boundServiceAccount is true when at least one workload pod spec now carries
	// serviceAccountName — either because this pass stamped it or because the
	// agent's own template already had it. It is false when the overlay wants an
	// SA but no pod-template-bearing workload was found to bind it to, which
	// would silently leave the pod on the namespace default.
	boundServiceAccount bool
	// renderedConfigVolumes is the set of pod volume names present across the
	// workloads. ConfigMounts render via the agent's own template (not this
	// post-render pass), so this is how the caller detects a mount whose volume
	// the template never emitted — the same silent-half-apply guard as the SA.
	renderedConfigVolumes map[string]bool
}

// applyPodOverlay binds the pod-template overlay into the rendered base
// manifests: it stamps serviceAccountName onto each workload's pod spec and
// merges the pod labels and annotations. Doing this centrally means an agent
// only has to pass the overlay parameter — it never re-implements the binding
// in its own template. The write is idempotent: a field the template already
// rendered is left untouched, so an agent mid-migration that still carries the
// binding does not double-render it.
func applyPodOverlay(_ context.Context, baseDir string, overlay *PodTemplateOverlay) (podOverlayResult, error) {
	result := podOverlayResult{renderedConfigVolumes: map[string]bool{}}
	if overlay == nil {
		return result, nil
	}
	if !overlay.HasServiceAccount() && len(overlay.PodLabels) == 0 && len(overlay.PodAnnotations) == 0 && !overlay.HasConfigMounts() && !overlay.HasContainers() {
		return result, nil
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return result, fmt.Errorf("read base dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		bound, volumes, err := applyPodOverlayToFile(filepath.Join(baseDir, entry.Name()), overlay)
		if err != nil {
			return result, err
		}
		if bound {
			result.boundServiceAccount = true
		}
		for _, name := range volumes {
			result.renderedConfigVolumes[name] = true
		}
	}
	return result, nil
}

func applyPodOverlayToFile(path string, overlay *PodTemplateOverlay) (bool, []string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, nil, fmt.Errorf("read manifest: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var documents []*yaml.Node
	var volumes []string
	changed := false
	bound := false
	for {
		var document yaml.Node
		err = decoder.Decode(&document)
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, nil, fmt.Errorf("parse manifest %s: %w", path, err)
		}
		if len(document.Content) == 0 {
			continue
		}
		documentChanged, documentBound, documentVolumes := applyPodOverlayToDocument(document.Content[0], overlay)
		changed = changed || documentChanged
		bound = bound || documentBound
		volumes = append(volumes, documentVolumes...)
		documents = append(documents, &document)
	}
	if !changed {
		return bound, volumes, nil
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	for _, document := range documents {
		if err = encoder.Encode(document); err != nil {
			return false, nil, fmt.Errorf("render manifest %s: %w", path, err)
		}
	}
	if err = encoder.Close(); err != nil {
		return false, nil, fmt.Errorf("render manifest %s: %w", path, err)
	}
	if err = os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return false, nil, err
	}
	return bound, volumes, nil
}

// applyPodOverlayToDocument patches a single workload document, returning
// whether it mutated the node, whether the pod spec carries serviceAccountName
// after the pass (already present or newly stamped), and the pod volume names
// the template rendered (used to detect config mounts the template dropped).
func applyPodOverlayToDocument(root *yaml.Node, overlay *PodTemplateOverlay) (changed, bound bool, volumes []string) {
	if root == nil || root.Kind != yaml.MappingNode {
		return false, false, nil
	}
	if !podTemplateWorkloadKinds[mappingScalar(root, "kind")] {
		return false, false, nil
	}
	template := mappingChild(mappingChild(root, "spec"), "template")
	if template == nil || template.Kind != yaml.MappingNode {
		return false, false, nil
	}
	podSpec := mappingChild(template, "spec")
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
		if podSpec != nil && podSpec.Kind == yaml.MappingNode {
			bound = true
			if setScalarIfAbsent(podSpec, "serviceAccountName", overlay.ServiceAccountName(), false) {
				changed = true
			}
		}
	}
	if overlay.HasContainers() && podSpec != nil && podSpec.Kind == yaml.MappingNode {
		if appendContainers(podSpec, overlay.Containers) {
			changed = true
		}
	}
	return changed, bound, podVolumeNames(podSpec)
}

// appendContainers merges the overlay's containers into the pod spec's
// containers sequence, skipping any whose name already exists so a re-render is
// idempotent and a template-declared container of the same name wins. It
// reports whether it added anything.
func appendContainers(podSpec *yaml.Node, containers []map[string]any) bool {
	seq := ensureSequenceChild(podSpec, "containers")
	existing := containerNames(seq)
	changed := false
	for _, container := range containers {
		name, _ := container["name"].(string)
		if name == "" || existing[name] {
			continue
		}
		node, err := mapToNode(container)
		if err != nil {
			continue // Validate() already rejected un-marshalable specs upstream
		}
		seq.Content = append(seq.Content, node)
		existing[name] = true
		changed = true
	}
	return changed
}

// ensureSequenceChild returns the sequence node at key, creating an empty one
// when absent so items can be appended.
func ensureSequenceChild(node *yaml.Node, key string) *yaml.Node {
	if child := mappingChild(node, key); child != nil {
		return child
	}
	value := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
	return value
}

// containerNames returns the set of container names already in a containers
// sequence, so a contribution never duplicates one.
func containerNames(seq *yaml.Node) map[string]bool {
	names := map[string]bool{}
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return names
	}
	for _, container := range seq.Content {
		if name := mappingScalar(container, "name"); name != "" {
			names[name] = true
		}
	}
	return names
}

// mapToNode marshals a raw container spec into a YAML mapping node so it can be
// appended into the rendered document.
func mapToNode(value map[string]any) (*yaml.Node, error) {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err = yaml.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	if document.Kind == yaml.DocumentNode && len(document.Content) == 1 {
		return document.Content[0], nil
	}
	return nil, fmt.Errorf("container did not encode to a single node")
}

// podVolumeNames returns the names of the volumes declared on a pod spec.
func podVolumeNames(podSpec *yaml.Node) []string {
	volumes := mappingChild(podSpec, "volumes")
	if volumes == nil || volumes.Kind != yaml.SequenceNode {
		return nil
	}
	var names []string
	for _, volume := range volumes.Content {
		if name := mappingScalar(volume, "name"); name != "" {
			names = append(names, name)
		}
	}
	return names
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
