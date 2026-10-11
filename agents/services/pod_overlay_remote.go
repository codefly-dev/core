package services

import (
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// The Kustomize build that validates a projection runs on the supplied
// filesystem, but Kustomize does not keep every load there: a reference it
// reads as a git repository is cloned by running git into a host temporary
// directory, and an http(s) file reference is fetched over the network. A
// filesystem confined to a render root says nothing about either. So every
// kustomization reachable from the base is read through the supplied
// filesystem first, and a remote reference refuses the projection before
// Kustomize sees it.

// kustomizationFileNames are the names Kustomize accepts for a kustomization.
var kustomizationFileNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// rootFields name entries Kustomize may open as new roots — the loads it
// clones when the reference parses as a repository.
var rootFields = []string{"resources", "bases", "components", "generators", "transformers", "validators"}

// textFields hold data Kustomize never loads, so a URL there is a value, not
// a reference.
var textFields = map[string]bool{
	"commonAnnotations": true, "commonLabels": true, "labels": true, "metadata": true,
	"patch": true, "buildMetadata": true, "images": true, "literals": true,
}

func refuseRemoteKustomizeReferences(filesystem filesys.FileSystem, baseDir string) error {
	seen := map[string]bool{}
	var walk func(dir string) error
	walk = func(dir string) error {
		dir = path.Clean(dir)
		if seen[dir] {
			return nil
		}
		seen[dir] = true
		file := ""
		for _, name := range kustomizationFileNames {
			if candidate := path.Join(dir, name); filesystem.Exists(candidate) {
				file = candidate
				break
			}
		}
		if file == "" {
			return nil
		}
		data, err := filesystem.ReadFile(file)
		if err != nil {
			return err
		}
		var kustomization map[string]any
		if err := yaml.Unmarshal(data, &kustomization); err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if err := refuseRemoteValues(file, "", kustomization); err != nil {
			return err
		}
		for _, field := range rootFields {
			entries, _ := kustomization[field].([]any)
			for _, entry := range entries {
				reference, ok := entry.(string)
				if !ok || strings.Contains(reference, "\n") {
					continue // an inline configuration, not a reference
				}
				if remoteRepository(reference) {
					return fmt.Errorf("%s: %s entry %q is a remote repository; a projection builds only from its render root", file, field, reference)
				}
				if child := path.Join(dir, reference); filesystem.IsDir(child) {
					if err := walk(child); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return walk(baseDir)
}

// refuseRemoteValues refuses any string a kustomization could fetch over
// http(s), outside the fields that only carry data.
func refuseRemoteValues(file, field string, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if textFields[key] {
				continue
			}
			if err := refuseRemoteValues(file, key, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := refuseRemoteValues(file, field, child); err != nil {
				return err
			}
		}
	case string:
		// A generator source may be "key=path".
		reference := typed
		if _, after, found := strings.Cut(reference, "="); found && (field == "files" || field == "envs") {
			reference = after
		}
		if remoteFile(reference) {
			return fmt.Errorf("%s: %s value %q is a remote reference; a projection builds only from its render root", file, field, typed)
		}
	}
	return nil
}

// remoteFile matches what Kustomize fetches over http(s).
func remoteFile(reference string) bool {
	lower := strings.ToLower(strings.TrimSpace(reference))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// remoteRepository is a superset of what Kustomize parses as a repository: a
// URL scheme, the forced git:: prefix, an SCP-style user@host:path, or the
// scheme-less github.com form. A plain relative path matches none of them.
func remoteRepository(reference string) bool {
	trimmed := strings.TrimSpace(reference)
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "://") || strings.HasPrefix(lower, "git::") ||
		strings.HasPrefix(lower, "github.com/") || strings.HasPrefix(lower, "github.com:") {
		return true
	}
	first := trimmed
	if index := strings.IndexAny(first, "/"); index >= 0 {
		first = first[:index]
	}
	return strings.Contains(first, "@") || strings.Contains(first, ":")
}
