package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"gopkg.in/yaml.v3"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// KubernetesManifestContractVersion identifies the policy recorded in deployment output.
const KubernetesManifestContractVersion = "codefly.dev/kubernetes-manifest/v1"

var unresolvedManifestValue = regexp.MustCompile(`\{\{|\}\}|\$\{[^}]+\}|(?i)\b(?:CHANGE_ME|REPLACE_ME)\b`)
var pinnedImage = regexp.MustCompile(`@sha256:[a-fA-F0-9]{64}$`)

var clusterScopedKinds = map[string]struct{}{
	"APIService":                       {},
	"CertificateSigningRequest":        {},
	"ClusterIssuer":                    {},
	"ClusterRole":                      {},
	"ClusterRoleBinding":               {},
	"CustomResourceDefinition":         {},
	"List":                             {},
	"MutatingWebhookConfiguration":     {},
	"Node":                             {},
	"PersistentVolume":                 {},
	"PriorityClass":                    {},
	"RuntimeClass":                     {},
	"StorageClass":                     {},
	"ValidatingAdmissionPolicy":        {},
	"ValidatingAdmissionPolicyBinding": {},
	"ValidatingWebhookConfiguration":   {},
}

var ownershipRequiredKinds = map[string]struct{}{
	"Namespace":      {},
	"Role":           {},
	"RoleBinding":    {},
	"ServiceAccount": {},
}

var restrictedVolumeSources = map[string]struct{}{
	"configMap":             {},
	"csi":                   {},
	"downwardAPI":           {},
	"emptyDir":              {},
	"ephemeral":             {},
	"persistentVolumeClaim": {},
	"projected":             {},
	"secret":                {},
}

var restrictedSELinuxTypes = map[string]struct{}{
	"":                   {},
	"container_engine_t": {},
	"container_t":        {},
	"container_init_t":   {},
	"container_kvm_t":    {},
}

var restrictedSysctls = map[string]struct{}{
	"kernel.shm_rmid_forced":              {},
	"net.ipv4.ip_local_port_range":        {},
	"net.ipv4.ip_local_reserved_ports":    {},
	"net.ipv4.ip_unprivileged_port_start": {},
	"net.ipv4.ping_group_range":           {},
	"net.ipv4.tcp_fin_timeout":            {},
	"net.ipv4.tcp_keepalive_intvl":        {},
	"net.ipv4.tcp_keepalive_probes":       {},
	"net.ipv4.tcp_keepalive_time":         {},
	"net.ipv4.tcp_syncookies":             {},
}

type kubernetesManifestObject struct {
	apiVersion string
	kind       string
	name       string
	namespace  string
	value      map[string]any
}

// ValidateKubernetesManifestTree builds the selected Kustomize overlay and
// applies the versioned static and optional server-side contract.
func ValidateKubernetesManifestTree(
	ctx context.Context,
	destination string,
	environment string,
	namespace string,
	profile builderv0.KubernetesOutputProfile,
	validateServerSide bool,
	validationKubeconfig string,
	validationContext string,
) *builderv0.KubernetesManifestValidation {
	result := &builderv0.KubernetesManifestValidation{
		StaticValidation:     builderv0.KubernetesManifestValidation_STATUS_PASSED,
		ServerSideValidation: builderv0.KubernetesManifestValidation_STATUS_NOT_RUN,
	}
	switch profile {
	case builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1:
	default:
		result.StaticValidation = builderv0.KubernetesManifestValidation_STATUS_FAILED
		result.Violations = []string{"unsupported Kubernetes output profile"}
		return result
	}

	manifest, violations := buildKustomizeManifest(destination, environment, profile)
	if len(violations) == 0 {
		violations = validateKubernetesManifest(manifest, namespace, profile)
	}
	if len(violations) > 0 {
		result.StaticValidation = builderv0.KubernetesManifestValidation_STATUS_FAILED
		result.Violations = stableViolations(violations)
		return result
	}

	if validateServerSide {
		if err := validateKubernetesManifestServerSide(
			ctx,
			manifest,
			namespace,
			validationKubeconfig,
			validationContext,
		); err != nil {
			result.ServerSideValidation = builderv0.KubernetesManifestValidation_STATUS_FAILED
			result.Violations = []string{err.Error()}
			return result
		}
		result.ServerSideValidation = builderv0.KubernetesManifestValidation_STATUS_PASSED
		result.ValidatedContext = validationContext
	}

	result.Promotable = profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 &&
		result.StaticValidation == builderv0.KubernetesManifestValidation_STATUS_PASSED &&
		result.ServerSideValidation == builderv0.KubernetesManifestValidation_STATUS_PASSED
	return result
}

func buildKustomizeManifest(
	destination string,
	environment string,
	profile builderv0.KubernetesOutputProfile,
) ([]byte, []string) {
	var violations []string
	err := filepath.WalkDir(destination, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		extension := strings.ToLower(filepath.Ext(path))
		if entry.IsDir() || (extension != ".yaml" && extension != ".yml" && extension != ".json") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if unresolvedManifestValue.Match(content) {
			relative, relativeErr := filepath.Rel(destination, path)
			if relativeErr != nil {
				relative = path
			}
			violations = append(violations, fmt.Sprintf("%s contains an unresolved placeholder", relative))
		}
		if profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 {
			decoder := yaml.NewDecoder(bytes.NewReader(content))
			for {
				var value map[string]any
				decodeErr := decoder.Decode(&value)
				if decodeErr == io.EOF {
					break
				}
				if decodeErr != nil {
					return fmt.Errorf("decode %s: %w", path, decodeErr)
				}
				kind, _ := value["kind"].(string)
				if kind == "Secret" {
					relative, relativeErr := filepath.Rel(destination, path)
					if relativeErr != nil {
						relative = path
					}
					violations = append(violations, fmt.Sprintf("%s emits a Secret resource", relative))
				}
				if kind == "ConfigMap" {
					object, _ := newKubernetesManifestObject(value, 0)
					if object != nil {
						violations = append(violations, object.validateConfigMap()...)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, []string{fmt.Sprintf("inspect rendered tree: %v", err)}
	}
	if len(violations) > 0 {
		return nil, violations
	}

	overlay := filepath.Join(destination, "overlays", environment)
	relative, err := filepath.Rel(destination, overlay)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, []string{"environment overlay resolves outside the deployment destination"}
	}
	options := krusty.MakeDefaultOptions()
	resources, err := krusty.MakeKustomizer(options).Run(filesys.MakeFsOnDisk(), overlay)
	if err != nil {
		return nil, []string{fmt.Sprintf("kustomize build: %v", err)}
	}
	manifest, err := resources.AsYaml()
	if err != nil {
		return nil, []string{fmt.Sprintf("serialize kustomize output: %v", err)}
	}
	if unresolvedManifestValue.Match(manifest) {
		return nil, []string{"kustomize output contains an unresolved placeholder"}
	}
	return manifest, nil
}

func validateKubernetesManifest(manifest []byte, namespace string, profile builderv0.KubernetesOutputProfile) []string {
	var violations []string
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	for document := 1; ; document++ {
		var value map[string]any
		err := decoder.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil {
			violations = append(violations, fmt.Sprintf("document %d is invalid YAML: %v", document, err))
			break
		}
		if len(value) == 0 {
			continue
		}
		object, objectViolations := newKubernetesManifestObject(value, document)
		violations = append(violations, objectViolations...)
		if object == nil {
			continue
		}
		violations = append(violations, object.validate(namespace, profile)...)
	}
	return stableViolations(violations)
}

func newKubernetesManifestObject(value map[string]any, document int) (*kubernetesManifestObject, []string) {
	apiVersion, _ := value["apiVersion"].(string)
	kind, _ := value["kind"].(string)
	metadata, metadataOK := mapValue(value, "metadata")
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	var violations []string
	if apiVersion == "" {
		violations = append(violations, fmt.Sprintf("document %d has no apiVersion", document))
	}
	if kind == "" {
		violations = append(violations, fmt.Sprintf("document %d has no kind", document))
	}
	if !metadataOK || name == "" {
		violations = append(violations, fmt.Sprintf("document %d has no metadata.name", document))
	}
	if len(violations) > 0 {
		return nil, violations
	}
	return &kubernetesManifestObject{
		apiVersion: apiVersion,
		kind:       kind,
		name:       name,
		namespace:  namespace,
		value:      value,
	}, nil
}

func (object *kubernetesManifestObject) validate(namespace string, profile builderv0.KubernetesOutputProfile) []string {
	var violations []string
	ref := object.reference()

	_, prohibitedClusterKind := clusterScopedKinds[object.kind]
	if prohibitedClusterKind || strings.HasPrefix(object.kind, "Cluster") {
		violations = append(violations, fmt.Sprintf("%s is a prohibited cluster-scoped resource", ref))
	}
	if object.kind == "Namespace" {
		if object.name != namespace {
			violations = append(violations, fmt.Sprintf("%s does not own target namespace %q", ref, namespace))
		}
	} else if !prohibitedClusterKind && !strings.HasPrefix(object.kind, "Cluster") && object.namespace != namespace {
		violations = append(violations, fmt.Sprintf("%s must set metadata.namespace to %q", ref, namespace))
	}
	if _, ownershipRequired := ownershipRequiredKinds[object.kind]; ownershipRequired && !object.isCodeflyOwned() {
		violations = append(violations, fmt.Sprintf("%s must set app.kubernetes.io/managed-by: codefly", ref))
	}

	if strings.Contains(strings.ToLower(object.kind), "secret") {
		violations = append(violations, object.validateSecretContract(profile)...)
	}
	if profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 && object.kind == "ConfigMap" {
		violations = append(violations, object.validateConfigMap()...)
	}
	if object.apiVersion == "rbac.authorization.k8s.io/v1" {
		violations = append(violations, object.validateRBAC()...)
	}
	if object.kind == "Service" {
		violations = append(violations, object.validateService()...)
	}
	podSpec, longRunning, hasPodSpec := object.podSpec()
	if hasPodSpec {
		violations = append(violations, object.validatePodAnnotations()...)
		violations = append(violations, object.validatePodSpec(podSpec, longRunning, profile)...)
	}
	return violations
}

func (object *kubernetesManifestObject) reference() string {
	return fmt.Sprintf("%s/%s", object.kind, object.name)
}

func (object *kubernetesManifestObject) isCodeflyOwned() bool {
	metadata, _ := mapValue(object.value, "metadata")
	labels, _ := mapValue(metadata, "labels")
	managedBy, _ := labels["app.kubernetes.io/managed-by"].(string)
	return strings.EqualFold(managedBy, "codefly")
}

func (object *kubernetesManifestObject) validateSecretContract(profile builderv0.KubernetesOutputProfile) []string {
	if profile != builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 {
		return nil
	}
	ref := object.reference()
	if object.kind != "ExternalSecret" || object.apiVersion != "external-secrets.io/v1" {
		return []string{fmt.Sprintf("%s is not an approved external secret contract", ref)}
	}
	spec, ok := mapValue(object.value, "spec")
	if !ok {
		return []string{fmt.Sprintf("%s has no spec", ref)}
	}
	store, ok := mapValue(spec, "secretStoreRef")
	if !ok || stringValue(store, "name") == "" {
		return []string{fmt.Sprintf("%s must reference a namespaced SecretStore", ref)}
	}
	if kind := stringValue(store, "kind"); kind != "" && kind != "SecretStore" {
		return []string{fmt.Sprintf("%s may only reference a namespaced SecretStore", ref)}
	}
	if target, ok := mapValue(spec, "target"); ok {
		if _, hasTemplate := target["template"]; hasTemplate {
			return []string{fmt.Sprintf("%s may not inline target template data", ref)}
		}
	}
	if _, hasData := spec["data"]; !hasData {
		if _, hasDataFrom := spec["dataFrom"]; !hasDataFrom {
			return []string{fmt.Sprintf("%s must declare data or dataFrom references", ref)}
		}
	}
	return nil
}

func (object *kubernetesManifestObject) validateConfigMap() []string {
	var violations []string
	for _, field := range []string{"data", "binaryData"} {
		values, ok := mapValue(object.value, field)
		if !ok {
			continue
		}
		for key := range values {
			if resourcesKeyIsSensitive(key) {
				violations = append(violations, fmt.Sprintf("%s.%s contains credential-like key %q", object.reference(), field, key))
			}
		}
	}
	return violations
}

func (object *kubernetesManifestObject) validateRBAC() []string {
	if object.kind != "Role" {
		return nil
	}
	var violations []string
	rules, _ := sliceValue(object.value, "rules")
	for index, rawRule := range rules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s.rules[%d] must be a mapping", object.reference(), index))
			continue
		}
		for _, field := range []string{"apiGroups", "resources", "verbs"} {
			values, _ := sliceValue(rule, field)
			for _, value := range values {
				if value == "*" {
					violations = append(violations, fmt.Sprintf("%s.rules[%d].%s may not contain wildcard access", object.reference(), index, field))
				}
			}
		}
	}
	return violations
}

func (object *kubernetesManifestObject) validateService() []string {
	spec, ok := mapValue(object.value, "spec")
	if !ok {
		return []string{fmt.Sprintf("%s has no spec", object.reference())}
	}
	var violations []string
	serviceType := stringValue(spec, "type")
	if serviceType != "" && serviceType != "ClusterIP" {
		violations = append(violations, fmt.Sprintf("%s uses unsafe service type %q", object.reference(), serviceType))
	}
	if externalIPs, ok := sliceValue(spec, "externalIPs"); ok && len(externalIPs) > 0 {
		violations = append(violations, fmt.Sprintf("%s sets externalIPs", object.reference()))
	}
	ports, _ := sliceValue(spec, "ports")
	for index, rawPort := range ports {
		port, ok := rawPort.(map[string]any)
		if ok {
			if _, hasNodePort := port["nodePort"]; hasNodePort {
				violations = append(violations, fmt.Sprintf("%s.spec.ports[%d] sets nodePort", object.reference(), index))
			}
		}
	}
	return violations
}

func (object *kubernetesManifestObject) podSpec() (map[string]any, bool, bool) {
	switch object.kind {
	case "Pod":
		spec, ok := mapValue(object.value, "spec")
		return spec, true, ok
	case "Deployment", "DaemonSet", "ReplicaSet", "ReplicationController", "StatefulSet":
		spec, ok := nestedMap(object.value, "spec", "template", "spec")
		return spec, true, ok
	case "Job":
		spec, ok := nestedMap(object.value, "spec", "template", "spec")
		return spec, false, ok
	case "CronJob":
		spec, ok := nestedMap(object.value, "spec", "jobTemplate", "spec", "template", "spec")
		return spec, false, ok
	default:
		spec, ok := nestedMap(object.value, "spec", "template", "spec")
		return spec, true, ok
	}
}

func (object *kubernetesManifestObject) validatePodAnnotations() []string {
	var metadata map[string]any
	switch object.kind {
	case "Pod":
		metadata, _ = mapValue(object.value, "metadata")
	case "CronJob":
		metadata, _ = nestedMap(object.value, "spec", "jobTemplate", "spec", "template", "metadata")
	default:
		metadata, _ = nestedMap(object.value, "spec", "template", "metadata")
	}
	annotations, _ := mapValue(metadata, "annotations")
	var violations []string
	for key, rawValue := range annotations {
		if !strings.HasPrefix(key, "container.apparmor.security.beta.kubernetes.io/") {
			continue
		}
		value, _ := rawValue.(string)
		if value != "runtime/default" && !strings.HasPrefix(value, "localhost/") {
			violations = append(violations, fmt.Sprintf("%s uses prohibited AppArmor annotation %q", object.reference(), value))
		}
	}
	return violations
}

func (object *kubernetesManifestObject) validatePodSpec(
	podSpec map[string]any,
	longRunning bool,
	profile builderv0.KubernetesOutputProfile,
) []string {
	ref := object.reference()
	var violations []string
	if automount, exists := podSpec["automountServiceAccountToken"].(bool); !exists || automount {
		violations = append(violations, fmt.Sprintf("%s must set automountServiceAccountToken: false", ref))
	}
	for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if boolValue(podSpec, field) {
			violations = append(violations, fmt.Sprintf("%s must not enable %s", ref, field))
		}
	}
	podSecurity, ok := mapValue(podSpec, "securityContext")
	if !ok || !boolValue(podSecurity, "runAsNonRoot") {
		violations = append(violations, fmt.Sprintf("%s pod securityContext must set runAsNonRoot: true", ref))
	}
	if !hasRestrictedSeccomp(podSecurity) {
		violations = append(violations, fmt.Sprintf("%s pod securityContext must use RuntimeDefault or Localhost seccomp", ref))
	}
	violations = append(violations, validateRestrictedSecurityContext(podSecurity, ref+" pod securityContext")...)
	sysctls, _ := sliceValue(podSecurity, "sysctls")
	for index, rawSysctl := range sysctls {
		sysctl, ok := rawSysctl.(map[string]any)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s pod securityContext.sysctls[%d] must be a mapping", ref, index))
			continue
		}
		name := stringValue(sysctl, "name")
		if _, allowed := restrictedSysctls[name]; !allowed {
			violations = append(violations, fmt.Sprintf("%s pod securityContext uses unsafe sysctl %q", ref, name))
		}
	}
	if longRunning && !positiveNumber(podSpec["terminationGracePeriodSeconds"]) {
		violations = append(violations, fmt.Sprintf("%s must set a positive terminationGracePeriodSeconds", ref))
	}

	volumes, _ := sliceValue(podSpec, "volumes")
	for index, rawVolume := range volumes {
		volume, ok := rawVolume.(map[string]any)
		if ok {
			for source := range volume {
				if source == "name" {
					continue
				}
				if _, allowed := restrictedVolumeSources[source]; !allowed {
					violations = append(violations, fmt.Sprintf("%s.spec.volumes[%d] uses prohibited volume type %q", ref, index, source))
				}
			}
		}
	}

	containers, _ := sliceValue(podSpec, "containers")
	if len(containers) == 0 {
		violations = append(violations, fmt.Sprintf("%s must define at least one container", ref))
	}
	for index, rawContainer := range containers {
		container, ok := rawContainer.(map[string]any)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s.spec.containers[%d] must be a mapping", ref, index))
			continue
		}
		violations = append(violations, object.validateContainer(container, index, false, longRunning, profile)...)
	}
	initContainers, _ := sliceValue(podSpec, "initContainers")
	for index, rawContainer := range initContainers {
		container, ok := rawContainer.(map[string]any)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s.spec.initContainers[%d] must be a mapping", ref, index))
			continue
		}
		violations = append(violations, object.validateContainer(container, index, true, false, profile)...)
	}
	if ephemeralContainers, ok := sliceValue(podSpec, "ephemeralContainers"); ok && len(ephemeralContainers) > 0 {
		violations = append(violations, fmt.Sprintf("%s may not include ephemeralContainers", ref))
	}
	return violations
}

func (object *kubernetesManifestObject) validateContainer(
	container map[string]any,
	index int,
	init bool,
	requireProbes bool,
	profile builderv0.KubernetesOutputProfile,
) []string {
	collection := "containers"
	if init {
		collection = "initContainers"
	}
	ref := fmt.Sprintf("%s.spec.%s[%d]", object.reference(), collection, index)
	var violations []string
	image := stringValue(container, "image")
	if image == "" {
		violations = append(violations, fmt.Sprintf("%s has no image", ref))
	} else if profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 && !pinnedImage.MatchString(image) {
		violations = append(violations, fmt.Sprintf("%s image %q is not pinned by sha256 digest", ref, image))
	}
	violations = append(violations, validateContainerSecurity(container, ref)...)
	violations = append(violations, validateContainerResources(container, ref)...)
	violations = append(violations, validateContainerPorts(container, ref)...)
	if profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 {
		violations = append(violations, validateContainerSecretReferences(container, ref)...)
	}
	violations = append(violations, validateContainerHealth(container, ref, requireProbes)...)
	return violations
}

func validateContainerSecurity(container map[string]any, ref string) []string {
	var violations []string
	security, ok := mapValue(container, "securityContext")
	if !ok {
		violations = append(violations, fmt.Sprintf("%s has no securityContext", ref))
		return violations
	}
	if boolValue(security, "privileged") {
		violations = append(violations, fmt.Sprintf("%s must not be privileged", ref))
	}
	if allow, exists := security["allowPrivilegeEscalation"].(bool); !exists || allow {
		violations = append(violations, fmt.Sprintf("%s must set allowPrivilegeEscalation: false", ref))
	}
	if !boolValue(security, "runAsNonRoot") {
		violations = append(violations, fmt.Sprintf("%s must set runAsNonRoot: true", ref))
	}
	if !boolValue(security, "readOnlyRootFilesystem") {
		violations = append(violations, fmt.Sprintf("%s must set readOnlyRootFilesystem: true", ref))
	}
	if !hasRestrictedSeccomp(security) {
		violations = append(violations, fmt.Sprintf("%s must use RuntimeDefault or Localhost seccomp", ref))
	}
	violations = append(violations, validateRestrictedSecurityContext(security, ref)...)
	capabilities, _ := mapValue(security, "capabilities")
	drop, _ := sliceValue(capabilities, "drop")
	if !containsString(drop, "ALL") {
		violations = append(violations, fmt.Sprintf("%s must drop ALL capabilities", ref))
	}
	add, _ := sliceValue(capabilities, "add")
	for _, capability := range add {
		if capability != "NET_BIND_SERVICE" {
			violations = append(violations, fmt.Sprintf("%s adds prohibited capability %q", ref, capability))
		}
	}
	if stringValue(security, "procMount") == "Unmasked" {
		violations = append(violations, fmt.Sprintf("%s uses an unmasked procMount", ref))
	} else if procMount := stringValue(security, "procMount"); procMount != "" && procMount != "Default" {
		violations = append(violations, fmt.Sprintf("%s uses prohibited procMount %q", ref, procMount))
	}
	return violations
}

func validateContainerResources(container map[string]any, ref string) []string {
	var violations []string
	resources, ok := mapValue(container, "resources")
	if !ok {
		violations = append(violations, fmt.Sprintf("%s has no resource requests or limits", ref))
	} else {
		for _, field := range []string{"requests", "limits"} {
			values, hasValues := mapValue(resources, field)
			if !hasValues || values["cpu"] == nil || values["memory"] == nil {
				violations = append(violations, fmt.Sprintf("%s.resources.%s must set cpu and memory", ref, field))
			}
		}
	}
	return violations
}

func validateContainerPorts(container map[string]any, ref string) []string {
	var violations []string
	ports, _ := sliceValue(container, "ports")
	for portIndex, rawPort := range ports {
		port, ok := rawPort.(map[string]any)
		if ok {
			if _, hasHostPort := port["hostPort"]; hasHostPort {
				violations = append(violations, fmt.Sprintf("%s.ports[%d] sets hostPort", ref, portIndex))
			}
			if hostIP := stringValue(port, "hostIP"); hostIP != "" {
				violations = append(violations, fmt.Sprintf("%s.ports[%d] sets hostIP", ref, portIndex))
			}
		}
	}
	return violations
}

func validateContainerSecretReferences(container map[string]any, ref string) []string {
	var violations []string
	envs, _ := sliceValue(container, "env")
	for envIndex, rawEnv := range envs {
		env, ok := rawEnv.(map[string]any)
		if !ok {
			continue
		}
		if valueFrom, ok := mapValue(env, "valueFrom"); ok {
			if secretKeyRef, ok := mapValue(valueFrom, "secretKeyRef"); ok {
				if stringValue(secretKeyRef, "name") == "" || stringValue(secretKeyRef, "key") == "" {
					violations = append(violations, fmt.Sprintf("%s.env[%d] has an incomplete secretKeyRef", ref, envIndex))
				}
			}
		}
		if resourcesKeyIsSensitive(stringValue(env, "name")) {
			if value, literal := env["value"]; literal && value != "" {
				violations = append(violations, fmt.Sprintf("%s.env[%d] contains a literal credential-like value", ref, envIndex))
			}
		}
	}
	envFrom, _ := sliceValue(container, "envFrom")
	for envFromIndex, rawSource := range envFrom {
		source, ok := rawSource.(map[string]any)
		if !ok {
			continue
		}
		if secretRef, ok := mapValue(source, "secretRef"); ok && stringValue(secretRef, "name") == "" {
			violations = append(violations, fmt.Sprintf("%s.envFrom[%d] has an incomplete secretRef", ref, envFromIndex))
		}
	}
	return violations
}

func validateContainerHealth(container map[string]any, ref string, requireProbes bool) []string {
	var violations []string
	if requireProbes {
		for _, probe := range []string{"startupProbe", "readinessProbe", "livenessProbe"} {
			if !validProbe(container[probe]) {
				violations = append(violations, fmt.Sprintf("%s must define a valid %s", ref, probe))
			}
		}
	}
	for _, probe := range []string{"startupProbe", "readinessProbe", "livenessProbe"} {
		if actionUsesHost(container[probe]) {
			violations = append(violations, fmt.Sprintf("%s.%s must not set a host", ref, probe))
		}
	}
	if lifecycle, ok := mapValue(container, "lifecycle"); ok {
		for _, hook := range []string{"postStart", "preStop"} {
			if actionUsesHost(lifecycle[hook]) {
				violations = append(violations, fmt.Sprintf("%s.lifecycle.%s must not set a host", ref, hook))
			}
		}
	}
	return violations
}

func validateKubernetesManifestServerSide(
	ctx context.Context,
	manifest []byte,
	namespace,
	kubeconfig,
	kubeContext string,
) error {
	if kubeconfig == "" {
		return fmt.Errorf("server-side validation requires an explicit kubeconfig")
	}
	if kubeContext == "" {
		return fmt.Errorf("server-side validation requires an explicit Kubernetes context")
	}
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("server-side validation requires kubectl: %w", err)
	}
	command := exec.CommandContext(
		ctx,
		kubectl,
		"--kubeconfig", kubeconfig,
		"--context", kubeContext,
		"apply",
		"--server-side",
		"--dry-run=server",
		"--validate=strict",
		"--namespace", namespace,
		"-f", "-",
	)
	command.Stdin = bytes.NewReader(manifest)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("server-side schema/admission validation failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func hasRestrictedSeccomp(security map[string]any) bool {
	seccomp, ok := mapValue(security, "seccompProfile")
	if !ok {
		return false
	}
	profile := stringValue(seccomp, "type")
	return profile == "RuntimeDefault" || profile == "Localhost"
}

func validateRestrictedSecurityContext(security map[string]any, ref string) []string {
	var violations []string
	if appArmor, ok := mapValue(security, "appArmorProfile"); ok {
		switch stringValue(appArmor, "type") {
		case "", "RuntimeDefault", "Localhost":
		case "Unconfined":
			violations = append(violations, fmt.Sprintf("%s must not use an unconfined AppArmor profile", ref))
		default:
			violations = append(violations, fmt.Sprintf("%s uses a prohibited AppArmor profile", ref))
		}
	}
	if seLinux, ok := mapValue(security, "seLinuxOptions"); ok {
		if user := stringValue(seLinux, "user"); user != "" {
			violations = append(violations, fmt.Sprintf("%s may not set SELinux user %q", ref, user))
		}
		if role := stringValue(seLinux, "role"); role != "" {
			violations = append(violations, fmt.Sprintf("%s may not set SELinux role %q", ref, role))
		}
		seLinuxType := stringValue(seLinux, "type")
		if _, allowed := restrictedSELinuxTypes[seLinuxType]; !allowed {
			violations = append(violations, fmt.Sprintf("%s uses prohibited SELinux type %q", ref, seLinuxType))
		}
	}
	if windows, ok := mapValue(security, "windowsOptions"); ok && boolValue(windows, "hostProcess") {
		violations = append(violations, fmt.Sprintf("%s must not enable Windows HostProcess", ref))
	}
	return violations
}

func actionUsesHost(value any) bool {
	action, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, kind := range []string{"httpGet", "tcpSocket"} {
		if handler, ok := mapValue(action, kind); ok && stringValue(handler, "host") != "" {
			return true
		}
	}
	return false
}

func validProbe(value any) bool {
	probe, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, handler := range []string{"exec", "grpc", "httpGet", "tcpSocket"} {
		if _, exists := probe[handler]; exists {
			return true
		}
	}
	return false
}

func resourcesKeyIsSensitive(key string) bool {
	return credentialLikeEnvironmentKey(key)
}

func credentialLikeEnvironmentKey(key string) bool {
	if separator := strings.LastIndex(key, "__"); separator >= 0 {
		key = key[separator+2:]
	}
	return key != "" && resources.IsSensitiveKey(key)
}

func stableViolations(violations []string) []string {
	unique := make(map[string]struct{}, len(violations))
	for _, violation := range violations {
		unique[violation] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for violation := range unique {
		result = append(result, violation)
	}
	sort.Strings(result)
	return result
}

func nestedMap(value map[string]any, path ...string) (map[string]any, bool) {
	current := value
	for _, field := range path {
		next, ok := mapValue(current, field)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func mapValue(value map[string]any, field string) (map[string]any, bool) {
	result, ok := value[field].(map[string]any)
	return result, ok
}

func sliceValue(value map[string]any, field string) ([]any, bool) {
	result, ok := value[field].([]any)
	return result, ok
}

func stringValue(value map[string]any, field string) string {
	result, _ := value[field].(string)
	return result
}

func boolValue(value map[string]any, field string) bool {
	result, _ := value[field].(bool)
	return result
}

func positiveNumber(value any) bool {
	switch number := value.(type) {
	case int:
		return number > 0
	case int64:
		return number > 0
	case uint64:
		return number > 0
	case float64:
		return number > 0
	default:
		return false
	}
}

func containsString(values []any, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
