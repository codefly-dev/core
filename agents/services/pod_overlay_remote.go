package services

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// The Kustomize build that validates a projection runs on the supplied
// filesystem, but Kustomize does not keep every load there: a reference it
// reads as a git repository is cloned by running git into a host temporary
// directory, and an http(s) file reference is fetched over the network. A
// filesystem confined to a render root says nothing about either. So every
// kustomization reachable from the base is read through the supplied
// filesystem first — decoded by Kustomize's own type, so a field is read under
// every spelling Kustomize accepts — and a remote reference refuses the
// projection before Kustomize sees it.

func refuseRemoteKustomizeReferences(filesystem filesys.FileSystem, baseDir string) error {
	walker := remoteReferenceWalker{filesystem: filesystem, seen: map[string]bool{}}
	return walker.kustomization(baseDir)
}

type remoteReferenceWalker struct {
	filesystem filesys.FileSystem
	seen       map[string]bool
}

func (w remoteReferenceWalker) kustomization(dir string) error {
	dir = path.Clean(dir)
	if w.seen[dir] {
		return nil
	}
	w.seen[dir] = true
	file := ""
	for _, name := range konfig.RecognizedKustomizationFileNames() {
		if candidate := path.Join(dir, name); w.filesystem.Exists(candidate) {
			file = candidate
			break
		}
	}
	if file == "" {
		return nil
	}
	data, err := w.filesystem.ReadFile(file)
	if err != nil {
		return err
	}
	var k types.Kustomization
	if err := k.Unmarshal(data); err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	k.FixKustomization()
	refuse := func(field, value string) error {
		return fmt.Errorf("%s: %s entry %q is a remote reference; a projection builds only from its render root", file, field, value)
	}
	// Resources are tried as a file (fetched when http) and then as a root
	// (cloned when it reads as a repository); components and bases are roots.
	for field, entries := range map[string][]string{"resources": k.Resources, "components": k.Components, "bases": k.Bases} {
		for _, entry := range entries {
			if remoteFile(entry) || remoteRepository(entry) {
				return refuse(field, entry)
			}
			if err := w.local(dir, entry); err != nil {
				return err
			}
		}
	}
	// A plugin entry that parses as configuration is inline; otherwise it is
	// a path, loaded as a resource is.
	for field, entries := range map[string][]string{"generators": k.Generators, "transformers": k.Transformers, "validators": k.Validators} {
		for _, entry := range entries {
			if documents, inline := inlineConfiguration(entry); inline {
				if err := refusePluginLoads(file, field, documents); err != nil {
					return err
				}
				continue
			}
			if remoteFile(entry) || remoteRepository(entry) {
				return refuse(field, entry)
			}
			if err := w.local(dir, entry); err != nil {
				return err
			}
		}
	}
	files := map[string][]string{
		"crds": k.Crds, "configurations": k.Configurations,
	}
	for _, patch := range k.PatchesStrategicMerge {
		files["patchesStrategicMerge"] = append(files["patchesStrategicMerge"], string(patch))
	}
	for _, patch := range append(append([]types.Patch{}, k.Patches...), k.PatchesJson6902...) {
		files["patches"] = append(files["patches"], patch.Path)
	}
	for _, replacement := range k.Replacements {
		files["replacements"] = append(files["replacements"], replacement.Path)
	}
	files["openapi"] = append(files["openapi"], k.OpenAPI["path"])
	sources := func(args types.GeneratorArgs) {
		for _, source := range args.FileSources {
			// A file source is [{key}=]{path}.
			if _, after, found := strings.Cut(source, "="); found {
				source = after
			}
			files["files"] = append(files["files"], source)
		}
		files["envs"] = append(append(files["envs"], args.EnvSources...), args.EnvSource)
	}
	for _, generator := range k.ConfigMapGenerator {
		sources(generator.GeneratorArgs)
	}
	for _, generator := range k.SecretGenerator {
		sources(generator.GeneratorArgs)
	}
	for field, entries := range files {
		for _, entry := range entries {
			if remoteFile(entry) {
				return refuse(field, entry)
			}
		}
	}
	return nil
}

// local follows a reference inside the supplied filesystem: a directory is a
// kustomization to walk, a file may hold plugin configurations whose own
// loads must stay local.
func (w remoteReferenceWalker) local(dir, entry string) error {
	target := path.Join(dir, entry)
	if w.filesystem.IsDir(target) {
		return w.kustomization(target)
	}
	if !w.filesystem.Exists(target) {
		return nil
	}
	data, err := w.filesystem.ReadFile(target)
	if err != nil {
		return err
	}
	documents, _ := decodeDocuments(data)
	return refusePluginLoads(target, "configuration", documents)
}

// inlineConfiguration reports whether a plugin entry is configuration rather
// than a path, as Kustomize decides it: it parses as resources.
func inlineConfiguration(entry string) ([]any, bool) {
	documents, err := decodeDocuments([]byte(entry))
	if err != nil || len(documents) == 0 {
		return nil, false
	}
	for _, document := range documents {
		mapping, ok := document.(map[string]any)
		if !ok || mapping["kind"] == nil {
			return nil, false
		}
	}
	return documents, true
}

func decodeDocuments(data []byte) ([]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var documents []any
	for {
		var document any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return documents, nil
		}
		if err != nil {
			return documents, err
		}
		if document != nil {
			documents = append(documents, document)
		}
	}
}

// refusePluginLoads refuses an http(s) string in a plugin configuration — a
// built-in generator or transformer loads its path, files and env files
// through the same loader — outside its metadata and annotation data.
func refusePluginLoads(file, field string, documents []any) error {
	var walk func(key string, value any) error
	walk = func(key string, value any) error {
		switch typed := value.(type) {
		case map[string]any:
			for child, nested := range typed {
				if strings.EqualFold(child, "metadata") || strings.EqualFold(child, "annotations") ||
					strings.EqualFold(child, "labels") || strings.EqualFold(child, "literals") {
					continue
				}
				if err := walk(child, nested); err != nil {
					return err
				}
			}
		case []any:
			for _, nested := range typed {
				if err := walk(key, nested); err != nil {
					return err
				}
			}
		case string:
			reference := typed
			if _, after, found := strings.Cut(reference, "="); found && strings.EqualFold(key, "files") {
				reference = after
			}
			if remoteFile(reference) {
				return fmt.Errorf("%s: %s value %q is a remote reference; a projection builds only from its render root", file, field, typed)
			}
		}
		return nil
	}
	for _, document := range documents {
		if err := walk("", document); err != nil {
			return err
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
	first, _, _ := strings.Cut(trimmed, "/")
	return strings.Contains(first, "@") || strings.Contains(first, ":")
}
