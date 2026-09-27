package services

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/codefly-dev/core/resources"
	"gopkg.in/yaml.v3"
)

const (
	// maxKubernetesObjectData is the API server's limit on one ConfigMap's or
	// Secret's data. A file carrier object above it would be refused at apply,
	// so the render refuses it first.
	maxKubernetesObjectData = 1 << 20

	fileCarrierVolume       = "codefly-configuration-files"
	secretFileCarrierVolume = "codefly-secret-configuration-files"
	fileCarrierManifest     = "configuration-files.yaml"

	// fileCarrierMode lets any user of the pod read a public file-delivered
	// value, whatever user the container runs as; it is public configuration,
	// the same bytes the ConfigMap would otherwise carry as environment.
	fileCarrierMode = 0o444
	// secretFileCarrierMode lets the pod's fsGroup read a secret file and
	// grants nothing to anyone else. The kubelet makes the volume's files
	// group-owned by the fsGroup and adds that group to every container's
	// supplemental groups, so the workload's user reads them whatever uid it
	// runs as (ensureSecretReadable).
	secretFileCarrierMode = 0o440
)

// FileCarrierDelivery is what a workload receives by file rather than as
// environment: the values of its ConfigMap and Secret volumes, keyed by the
// environment key each value would otherwise have been delivered under.
type FileCarrierDelivery struct {
	ConfigMap map[string]string
	Secret    map[string]string
}

func (d *FileCarrierDelivery) empty() bool {
	return d == nil || (len(d.ConfigMap) == 0 && len(d.Secret) == 0)
}

// splitFileCarriers separates the values delivered inline from those
// delivered by file (resources.EnvironmentVariable.File). Each file-delivered
// value is replaced inline by its path carrier, pointing into the volume it
// is mounted from; files receives its content.
func splitFileCarriers(envs []*resources.EnvironmentVariable, mount string, files map[string]string) ([]*resources.EnvironmentVariable, error) {
	var inline []*resources.EnvironmentVariable
	for _, env := range envs {
		if env == nil {
			continue
		}
		if !env.File {
			inline = append(inline, env)
			continue
		}
		if _, taken := files[env.Key]; taken {
			return nil, fmt.Errorf("%s is delivered by file twice", env.Key)
		}
		files[env.Key] = env.ValueAsString()
		inline = append(inline, &resources.EnvironmentVariable{Key: resources.FileCarrierKey(env.Key), Value: path.Join(mount, env.Key), Secret: env.Secret})
	}
	return inline, nil
}

func checkObjectData(kind, name string, data map[string]string) error {
	total := 0
	for key, value := range data {
		total += len(key) + len(value)
	}
	if total > maxKubernetesObjectData {
		return fmt.Errorf("%s %s would carry %d bytes of file-delivered configuration, above the %d-byte object limit", kind, name, total, maxKubernetesObjectData)
	}
	return nil
}

// emitFileCarriers writes the workload's file-delivered values as a ConfigMap
// (and, when any is secret, a Secret) into the environment overlay, adds that
// manifest to the overlay's kustomization, and mounts both read-only into
// every container of every workload in the base. Nothing is emitted when no
// value is delivered by file, so such a workload renders exactly as before.
func emitFileCarriers(baseDir, overlayDir, namespace, service string, delivery *FileCarrierDelivery) error {
	if delivery.empty() {
		return nil
	}
	configMapName, secretName := "cmf-"+service, "secretf-"+service
	if err := checkObjectData("ConfigMap", configMapName, delivery.ConfigMap); err != nil {
		return err
	}
	if err := checkObjectData("Secret", secretName, delivery.Secret); err != nil {
		return err
	}
	var manifest bytes.Buffer
	encoder := yaml.NewEncoder(&manifest)
	encoder.SetIndent(2)
	if len(delivery.ConfigMap) > 0 {
		data, binary := map[string]string{}, map[string]string{}
		for key, value := range delivery.ConfigMap {
			if utf8.ValidString(value) {
				data[key] = value
			} else {
				binary[key] = base64.StdEncoding.EncodeToString([]byte(value))
			}
		}
		object := map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": configMapName, "namespace": namespace},
		}
		if len(data) > 0 {
			object["data"] = data
		}
		if len(binary) > 0 {
			object["binaryData"] = binary
		}
		if err := encoder.Encode(object); err != nil {
			return err
		}
	}
	if len(delivery.Secret) > 0 {
		data := map[string]string{}
		for key, value := range delivery.Secret {
			data[key] = base64.StdEncoding.EncodeToString([]byte(value))
		}
		if err := encoder.Encode(map[string]any{
			"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
			"metadata": map[string]any{"name": secretName, "namespace": namespace},
			"data":     data,
		}); err != nil {
			return err
		}
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(overlayDir, fileCarrierManifest), manifest.Bytes(), 0o644); err != nil {
		return err
	}
	if err := addKustomizeResource(filepath.Join(overlayDir, "kustomization.yaml"), fileCarrierManifest); err != nil {
		return err
	}
	var volumes []fileCarrierMount
	if len(delivery.ConfigMap) > 0 {
		volumes = append(volumes, fileCarrierMount{name: fileCarrierVolume, mountPath: resources.KubernetesFileCarrierMount, configMap: configMapName})
	}
	if len(delivery.Secret) > 0 {
		volumes = append(volumes, fileCarrierMount{name: secretFileCarrierVolume, mountPath: resources.KubernetesSecretFileCarrierMount, secret: secretName})
	}
	mounted, err := mountFileCarriers(baseDir, volumes)
	if err != nil {
		return err
	}
	if !mounted {
		return fmt.Errorf("service %s delivers configuration by file but its render carries no workload to mount it into", service)
	}
	return nil
}

type fileCarrierMount struct {
	name, mountPath, configMap, secret string
}

// mountFileCarriers adds the carrier volumes to every workload pod template in
// baseDir and a read-only mount of each to every container. It reports whether
// any workload received them.
func mountFileCarriers(baseDir string, volumes []fileCarrierMount) (bool, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return false, err
	}
	mounted := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		file := filepath.Join(baseDir, entry.Name())
		content, err := os.ReadFile(file)
		if err != nil {
			return false, err
		}
		decoder := yaml.NewDecoder(bytes.NewReader(content))
		var documents []*yaml.Node
		changed := false
		for {
			var document yaml.Node
			if err = decoder.Decode(&document); err == io.EOF {
				break
			} else if err != nil {
				return false, fmt.Errorf("parse manifest %s: %w", file, err)
			}
			if len(document.Content) > 0 {
				if patched, err := mountIntoWorkload(document.Content[0], volumes); err != nil {
					return false, fmt.Errorf("%s: %w", file, err)
				} else if patched {
					changed = true
				}
			}
			documents = append(documents, &document)
		}
		if !changed {
			continue
		}
		mounted = true
		var out bytes.Buffer
		encoder := yaml.NewEncoder(&out)
		encoder.SetIndent(2)
		for _, document := range documents {
			if err = encoder.Encode(document); err != nil {
				return false, err
			}
		}
		if err = encoder.Close(); err != nil {
			return false, err
		}
		if err = os.WriteFile(file, out.Bytes(), 0o644); err != nil {
			return false, err
		}
	}
	return mounted, nil
}

func mountIntoWorkload(root *yaml.Node, volumes []fileCarrierMount) (bool, error) {
	template := workloadPodTemplate(root)
	if template == nil || template.Kind != yaml.MappingNode {
		return false, nil
	}
	spec := mappingChild(template, "spec")
	if spec == nil || spec.Kind != yaml.MappingNode {
		return false, nil
	}
	for _, volume := range volumes {
		if volume.secret != "" {
			if err := ensureSecretReadable(spec); err != nil {
				return false, err
			}
		}
	}
	podVolumes := ensureSequenceChild(spec, "volumes")
	for _, volume := range volumes {
		for _, existing := range podVolumes.Content {
			if mappingScalar(existing, "name") == volume.name {
				return false, fmt.Errorf("the workload already declares volume %q", volume.name)
			}
		}
		source := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		sourceKind := "configMap"
		if volume.secret != "" {
			sourceKind = "secret"
			appendScalar(source, "secretName", volume.secret)
			appendInt(source, "defaultMode", secretFileCarrierMode)
		} else {
			appendScalar(source, "name", volume.configMap)
			appendInt(source, "defaultMode", fileCarrierMode)
		}
		entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendScalar(entry, "name", volume.name)
		entry.Content = append(entry.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: sourceKind}, source)
		podVolumes.Content = append(podVolumes.Content, entry)
	}
	containers := mappingChild(spec, "containers")
	if containers == nil || containers.Kind != yaml.SequenceNode || len(containers.Content) == 0 {
		return false, fmt.Errorf("the workload declares no container to mount file-delivered configuration into")
	}
	for _, container := range containers.Content {
		mounts := ensureSequenceChild(container, "volumeMounts")
		for _, volume := range volumes {
			mount := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendScalar(mount, "name", volume.name)
			appendScalar(mount, "mountPath", volume.mountPath)
			mount.Content = append(mount.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "readOnly"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
			mounts.Content = append(mounts.Content, mount)
		}
	}
	return true, nil
}

func ensureSequenceChild(node *yaml.Node, key string) *yaml.Node {
	if child := mappingChild(node, key); child != nil {
		if child.Kind != yaml.SequenceNode {
			child.Kind, child.Tag, child.Value, child.Content = yaml.SequenceNode, "!!seq", "", nil
		}
		return child
	}
	value := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return value
}

func appendScalar(node *yaml.Node, key, value string) {
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func appendInt(node *yaml.Node, key string, value int) {
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", value)})
}

// ensureSecretReadable makes a secret volume readable by the workload's user
// and nobody else. A secret volume's files are owned by root; with mode 0440
// only their group reads them, and that group is the pod's fsGroup. A pod
// that declares one keeps it. Otherwise the fsGroup is the group the pod runs
// as — its runAsGroup, else its runAsUser — declared on the pod or, the same
// on every container, on its containers. A pod that says neither cannot be
// given a secret file its user can read without making it world-readable, so
// the render is refused.
func ensureSecretReadable(spec *yaml.Node) error {
	security := mappingChild(spec, "securityContext")
	if security != nil && mappingScalar(security, "fsGroup") != "" {
		return nil
	}
	group := ""
	if security != nil {
		group = firstNonEmpty(mappingScalar(security, "runAsGroup"), mappingScalar(security, "runAsUser"))
	}
	if group == "" {
		if containers := mappingChild(spec, "containers"); containers != nil {
			for _, container := range containers.Content {
				context := mappingChild(container, "securityContext")
				candidate := ""
				if context != nil {
					candidate = firstNonEmpty(mappingScalar(context, "runAsGroup"), mappingScalar(context, "runAsUser"))
				}
				if candidate == "" || (group != "" && candidate != group) {
					group = ""
					break
				}
				group = candidate
			}
		}
	}
	if group == "" {
		return fmt.Errorf("a secret delivered by file needs the pod to declare the user it runs as (securityContext fsGroup, runAsGroup or runAsUser): without one the file is readable either by no one or by everyone")
	}
	if security == nil {
		security = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		spec.Content = append(spec.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "securityContext"}, security)
	}
	security.Content = append(security.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "fsGroup"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: group})
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
