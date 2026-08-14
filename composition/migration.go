package composition

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	V1MigrationDrop    V1MigrationClassification = "drop"
	V1MigrationBlocked V1MigrationClassification = "blocked"
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

func PlanV1Migration(moduleDir, newBaseDir string, bridge *V1Bridge) ([]V1MigrationEntry, error) {
	if bridge == nil || bridge.Manifest == nil {
		return nil, errors.New("v1 base manifest is required")
	}
	paths := make([]string, 0, len(bridge.Manifest.Files))
	for path := range bridge.Manifest.Files {
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
		oldDigest := bridge.Manifest.Files[path]
		entry := V1MigrationEntry{Path: path, OldDigest: oldDigest, ConsumerDigest: consumerDigest, NewDigest: newDigest, Classification: V1MigrationBlocked}
		if (consumerExists && (consumerDigest == oldDigest || consumerDigest == newDigest)) || (!consumerExists && !newExists) {
			entry.Classification = V1MigrationDrop
		}
		entries = append(entries, entry)
	}
	return entries, nil
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
