package sbom

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type packageLock struct {
	Name            string                        `json:"name"`
	Version         string                        `json:"version"`
	LockfileVersion int                           `json:"lockfileVersion"`
	Packages        map[string]packageLockPackage `json:"packages"`
}

type packageLockPackage struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Dev          bool              `json:"dev"`
	License      string            `json:"license"`
	Integrity    string            `json:"integrity"`
	Dependencies map[string]string `json:"dependencies"`
}

// Node inventories package-lock.json without executing lifecycle scripts or
// consulting the network. Lockfile v2+ is required because older files do not
// carry an authoritative physical package graph.
func Node(_ context.Context, dir string, includeDev bool) (*Result, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package-lock.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: package-lock.json is required", ErrUnsupported)
		}
		return nil, err
	}
	var lock packageLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parse package-lock.json: %w", err)
	}
	if lock.LockfileVersion < 2 || len(lock.Packages) == 0 {
		return nil, fmt.Errorf("%w: package-lock v2 or newer is required", ErrUnsupported)
	}
	return packageLockResult(&lock, includeDev)
}

func packageLockResult(lock *packageLock, includeDev bool) (*Result, error) {
	rootEntry := lock.Packages[""]
	rootName := first(rootEntry.Name, lock.Name)
	rootVersion := first(rootEntry.Version, lock.Version)
	root := nodeComponent(rootName, rootVersion, rootEntry, agentv0.ComponentType_MODULE)
	root.BomRef = first(root.Purl, "application:"+rootName)

	componentsByPath := map[string]*agentv0.Component{}
	var components []*agentv0.Component
	paths := make([]string, 0, len(lock.Packages))
	for packagePath := range lock.Packages {
		if packagePath != "" {
			paths = append(paths, packagePath)
		}
	}
	sort.Strings(paths)
	for _, packagePath := range paths {
		entry := lock.Packages[packagePath]
		if entry.Dev && !includeDev {
			continue
		}
		name := first(entry.Name, nodeNameFromPath(packagePath))
		if name == "" || entry.Version == "" {
			continue
		}
		component := nodeComponent(name, entry.Version, entry, agentv0.ComponentType_LIBRARY)
		componentsByPath[packagePath] = component
		components = append(components, component)
	}

	dependencies := make([]*agentv0.Dependency, 0, len(components)+1)
	dependencies = append(dependencies, &agentv0.Dependency{
		Ref:       root.BomRef,
		DependsOn: nodeDependencyRefs(lock.Packages, componentsByPath, "", rootEntry.Dependencies),
	})
	for _, packagePath := range paths {
		component, ok := componentsByPath[packagePath]
		if !ok {
			continue
		}
		dependencies = append(dependencies, &agentv0.Dependency{
			Ref:       component.BomRef,
			DependsOn: nodeDependencyRefs(lock.Packages, componentsByPath, packagePath, lock.Packages[packagePath].Dependencies),
		})
	}
	return finish(root, components, dependencies, fmt.Sprintf("package-lock-v%d", lock.LockfileVersion), "TYPESCRIPT")
}

func nodeComponent(name, version string, entry packageLockPackage, componentType agentv0.ComponentType) *agentv0.Component {
	group, shortName := splitName(name)
	purlName := name
	if strings.HasPrefix(purlName, "@") {
		purlName = "%40" + strings.TrimPrefix(purlName, "@")
	}
	purl := "pkg:npm/" + purlName
	if version != "" {
		purl += "@" + version
	}
	component := &agentv0.Component{
		Name:    shortName,
		Group:   group,
		Version: version,
		Type:    componentType,
		Purl:    purl,
		BomRef:  purl,
	}
	if entry.License != "" {
		component.Licenses = []string{entry.License}
	}
	if hash := integrityHash(entry.Integrity); hash != nil {
		component.Hashes = []*agentv0.Hash{hash}
	}
	return component
}

func nodeNameFromPath(packagePath string) string {
	const marker = "node_modules/"
	if i := strings.LastIndex(packagePath, marker); i >= 0 {
		return packagePath[i+len(marker):]
	}
	return ""
}

func nodeDependencyRefs(all map[string]packageLockPackage, included map[string]*agentv0.Component, packagePath string, declared map[string]string) []string {
	refs := make([]string, 0, len(declared))
	for name := range declared {
		resolved := resolveNodePackage(all, packagePath, name)
		if component := included[resolved]; component != nil {
			refs = append(refs, componentRef(component))
		}
	}
	return refs
}

func resolveNodePackage(all map[string]packageLockPackage, packagePath, name string) string {
	base := packagePath
	for {
		candidate := "node_modules/" + name
		if base != "" {
			candidate = base + "/node_modules/" + name
		}
		if _, ok := all[candidate]; ok {
			return candidate
		}
		if base == "" {
			return ""
		}
		if i := strings.LastIndex(base, "/node_modules/"); i >= 0 {
			base = base[:i]
		} else {
			base = ""
		}
	}
}

func integrityHash(value string) *agentv0.Hash {
	algorithm, encoded, ok := strings.Cut(value, "-")
	if !ok {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	var canonical string
	switch strings.ToLower(algorithm) {
	case "sha256":
		if len(raw) != sha256.Size {
			return nil
		}
		canonical = "SHA-256"
	case "sha512":
		if len(raw) != sha512.Size {
			return nil
		}
		canonical = "SHA-512"
	default:
		return nil
	}
	return &agentv0.Hash{Algorithm: canonical, Content: hex.EncodeToString(raw)}
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
