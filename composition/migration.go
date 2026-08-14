package composition

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type V1BaseSource struct {
	Schema       string `json:"schema"`
	Repository   string `json:"repository"`
	Ref          string `json:"ref"`
	Commit       string `json:"commit"`
	Subdirectory string `json:"subdirectory"`
}

type V1BaseManifest struct {
	Note      string            `json:"note,omitempty"`
	FileCount int               `json:"fileCount"`
	Files     map[string]string `json:"files"`
}

type V1Bridge struct {
	Source   *V1BaseSource
	Manifest *V1BaseManifest
}

type V1MigrationClassification string

const (
	V1MigrationDrop         V1MigrationClassification = "drop"
	V1MigrationUpstream     V1MigrationClassification = "upstream"
	V1MigrationContribution V1MigrationClassification = "contribution"
	V1MigrationGenerated    V1MigrationClassification = "generated"
	V1MigrationBlocked      V1MigrationClassification = "blocked"
)

type V1MigrationEntry struct {
	Path           string                    `json:"path"`
	OldDigest      string                    `json:"oldDigest"`
	ConsumerDigest string                    `json:"consumerDigest,omitempty"`
	NewDigest      string                    `json:"newDigest,omitempty"`
	Classification V1MigrationClassification `json:"classification"`
}

func LoadV1Bridge(moduleDir string) (*V1Bridge, error) {
	locations := []string{moduleDir, filepath.Join(moduleDir, "tools")}
	var sourceData, manifestData []byte
	var err error
	for _, location := range locations {
		if sourceData == nil {
			sourceData, err = os.ReadFile(filepath.Join(location, "base-source.json"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if errors.Is(err, os.ErrNotExist) {
				sourceData = nil
			}
		}
		if manifestData == nil {
			manifestData, err = os.ReadFile(filepath.Join(location, "base-manifest.json"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if errors.Is(err, os.ErrNotExist) {
				manifestData = nil
			}
		}
	}
	if sourceData == nil || manifestData == nil {
		return nil, os.ErrNotExist
	}
	var source V1BaseSource
	if err = decodeStrictJSON(sourceData, &source); err != nil {
		return nil, fmt.Errorf("decode v1 base source: %w", err)
	}
	var manifest V1BaseManifest
	if err = decodeStrictJSON(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("decode v1 base manifest: %w", err)
	}
	if source.Schema != "codefly/base-source/v1" || source.Repository == "" || source.Ref == "" || !commitPattern.MatchString(source.Commit) {
		return nil, errors.New("v1 base source is invalid")
	}
	if manifest.FileCount != len(manifest.Files) {
		return nil, errors.New("v1 base manifest file count does not match files")
	}
	for path, digest := range manifest.Files {
		if err := validateRelativePath("v1 base manifest", path); err != nil {
			return nil, err
		}
		if !digestPattern.MatchString("sha256:" + digest) {
			return nil, fmt.Errorf("v1 base manifest digest for %s is invalid", path)
		}
	}
	return &V1Bridge{Source: &source, Manifest: &manifest}, nil
}

func PlanV1Migration(moduleDir, newBaseDir string, bridge *V1Bridge, ownership map[string]V1MigrationClassification) ([]V1MigrationEntry, error) {
	if bridge == nil || bridge.Manifest == nil {
		return nil, errors.New("v1 base manifest is required")
	}
	pathsByName := make(map[string]struct{}, len(bridge.Manifest.Files))
	for path := range bridge.Manifest.Files {
		pathsByName[path] = struct{}{}
	}
	for path, classification := range ownership {
		if err := validateRelativePath("v1 migration ownership", path); err != nil {
			return nil, err
		}
		switch classification {
		case V1MigrationUpstream, V1MigrationContribution, V1MigrationGenerated:
		default:
			return nil, fmt.Errorf("v1 migration ownership for %s has invalid classification %q", path, classification)
		}
	}
	consumerPaths, err := migrationTreePaths(moduleDir, true)
	if err != nil {
		return nil, err
	}
	newPaths, err := migrationTreePaths(newBaseDir, false)
	if err != nil {
		return nil, err
	}
	for path := range consumerPaths {
		pathsByName[path] = struct{}{}
	}
	for path := range newPaths {
		pathsByName[path] = struct{}{}
	}
	paths := make([]string, 0, len(pathsByName))
	for path := range pathsByName {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	entries := make([]V1MigrationEntry, 0, len(paths))
	for _, path := range paths {
		consumerDigest, consumerExists, err := migrationFileDigest(filepath.Join(moduleDir, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		newDigest, newExists, err := migrationFileDigest(filepath.Join(newBaseDir, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		oldDigest, oldExists := bridge.Manifest.Files[path]
		entry := V1MigrationEntry{Path: path, OldDigest: oldDigest, ConsumerDigest: consumerDigest, NewDigest: newDigest, Classification: V1MigrationBlocked}
		if (consumerExists && ((oldExists && consumerDigest == oldDigest) || (newExists && consumerDigest == newDigest))) ||
			(!consumerExists && !newExists) || (!oldExists && !consumerExists) {
			entry.Classification = V1MigrationDrop
		} else if classification, exists := ownership[path]; exists {
			entry.Classification = classification
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func migrationTreePaths(root string, excludeBridge bool) (map[string]struct{}, error) {
	paths := make(map[string]struct{})
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if excludeBridge && isV1BridgePath(relative) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("v1 migration path %s contains a symlink", relative)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("v1 migration path %s is not a regular file", relative)
		}
		paths[relative] = struct{}{}
		return nil
	})
	return paths, err
}

func isV1BridgePath(path string) bool {
	leaf := strings.TrimPrefix(path, "tools/")
	return leaf == "base-source.json" || leaf == "base-manifest.json" || leaf == "base-integrity-allow.json"
}

func migrationFileDigest(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("v1 migration path %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest), true, nil
}
