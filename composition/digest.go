package composition

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func CompositionDigest(moduleDir string, descriptor *Descriptor) (string, error) {
	if err := descriptor.Validate(); err != nil {
		return "", err
	}
	hash := sha256.New()
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	writeDigestFrame(hash, []byte("descriptor"))
	writeDigestFrame(hash, descriptorJSON)
	for _, contribution := range contributionPaths(descriptor) {
		writeDigestFrame(hash, []byte(contribution.Kind))
		writeDigestFrame(hash, []byte(contribution.Identity))
		writeDigestFrame(hash, []byte(contribution.Path))
		if err := hashContribution(hash, filepath.Join(moduleDir, filepath.FromSlash(contribution.Path))); err != nil {
			return "", fmt.Errorf("hash %s contribution %s: %w", contribution.Kind, contribution.Path, err)
		}
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

type contributionPath struct {
	Kind     string
	Identity string
	Path     string
}

func contributionPaths(descriptor *Descriptor) []contributionPath {
	var paths []contributionPath
	for _, contribution := range descriptor.Contributions.Frontend {
		paths = append(paths, contributionPath{Kind: "frontend", Identity: contribution.Export, Path: contribution.Path})
	}
	for _, contribution := range descriptor.Contributions.Settings {
		paths = append(paths, contributionPath{Kind: "settings", Identity: contribution.Message, Path: contribution.Path})
	}
	for _, contribution := range descriptor.Contributions.Permissions {
		paths = append(paths, contributionPath{Kind: "permissions", Identity: contribution.Path, Path: contribution.Path})
	}
	for _, contribution := range descriptor.Contributions.Fixtures {
		paths = append(paths, contributionPath{Kind: "fixtures", Identity: contribution.Path, Path: contribution.Path})
	}
	for _, contribution := range descriptor.Contributions.Tests {
		paths = append(paths, contributionPath{Kind: "tests", Identity: contribution.Path, Path: contribution.Path})
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Kind != paths[j].Kind {
			return paths[i].Kind < paths[j].Kind
		}
		if paths[i].Identity != paths[j].Identity {
			return paths[i].Identity < paths[j].Identity
		}
		return paths[i].Path < paths[j].Path
	})
	return paths
}

func hashContribution(destination io.Writer, source string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errorsUnsafeContribution(source)
	}
	if info.Mode().IsRegular() {
		writeDigestFrame(destination, []byte("file"))
		return hashContributionFile(destination, source)
	}
	if !info.IsDir() {
		return errorsUnsafeContribution(source)
	}
	var names []string
	err = filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return errorsUnsafeContribution(current)
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		info, err := os.Stat(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		writeDigestFrame(destination, []byte(name))
		if info.IsDir() {
			writeDigestFrame(destination, []byte("directory"))
			continue
		}
		writeDigestFrame(destination, []byte("file"))
		if err := hashContributionFile(destination, filepath.Join(source, filepath.FromSlash(name))); err != nil {
			return err
		}
	}
	return nil
}

func hashContributionFile(destination io.Writer, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	mode := []byte("0644")
	if info.Mode().Perm()&0o111 != 0 {
		mode = []byte("0755")
	}
	writeDigestFrame(destination, mode)
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(info.Size()))
	_, _ = destination.Write(size[:])
	_, copyErr := io.Copy(destination, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func contributionDigest(source string) (string, error) {
	hash := sha256.New()
	if err := hashContribution(hash, source); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func writeDigestFrame(destination io.Writer, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write(value)
}

func errorsUnsafeContribution(path string) error {
	return fmt.Errorf("contribution contains symlink or non-regular path %q", path)
}
