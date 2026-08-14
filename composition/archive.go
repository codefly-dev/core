package composition

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultMaxArchiveEntries = 100_000
	defaultMaxExpandedBytes  = int64(4 << 30)
)

func CanonicalArchive(root string) ([]byte, string, error) {
	var buffer bytes.Buffer
	if err := WriteCanonicalArchive(root, &buffer); err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(buffer.Bytes())
	return buffer.Bytes(), fmt.Sprintf("sha256:%x", digest), nil
}

func WriteCanonicalArchive(root string, destination io.Writer) error {
	return writeCanonicalArchive(root, destination, nil)
}

func writeCanonicalArchive(root string, destination io.Writer, excluded map[string]struct{}) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	var names []string
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
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
		if _, skip := excluded[filepath.ToSlash(relative)]; skip {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("canonical module archive cannot contain symlink %s", current)
		}
		if !entry.Type().IsRegular() && !entry.IsDir() {
			return fmt.Errorf("canonical module archive cannot contain non-regular path %s", current)
		}
		names = append(names, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(names)
	writer := tar.NewWriter(destination)
	for _, name := range names {
		if err := writeCanonicalEntry(writer, root, name); err != nil {
			_ = writer.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close canonical module archive: %w", err)
	}
	return nil
}

func canonicalCacheDigest(root string) (string, error) {
	hash := sha256.New()
	if err := writeCanonicalArchive(root, hash, map[string]struct{}{cacheMarkerName: {}}); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func writeCanonicalEntry(writer *tar.Writer, root, name string) error {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	header := &tar.Header{
		Name:       name,
		Uid:        0,
		Gid:        0,
		Uname:      "",
		Gname:      "",
		ModTime:    time.Unix(0, 0),
		AccessTime: time.Time{},
		ChangeTime: time.Time{},
		Format:     tar.FormatPAX,
	}
	if info.IsDir() {
		header.Typeflag = tar.TypeDir
		header.Mode = 0o755
		header.Name += "/"
	} else {
		header.Typeflag = tar.TypeReg
		header.Size = info.Size()
		header.Mode = 0o644
		if info.Mode().Perm()&0o111 != 0 {
			header.Mode = 0o755
		}
	}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write canonical archive header %s: %w", name, err)
	}
	if info.IsDir() {
		return nil
	}
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(writer, file)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write canonical archive entry %s: %w", name, copyErr)
	}
	return closeErr
}

func ExtractArchive(ctx context.Context, archive []byte, destination string) error {
	return extractArchive(ctx, archive, destination, defaultMaxArchiveEntries, defaultMaxExpandedBytes)
}

func extractArchive(ctx context.Context, archive []byte, destination string, maxEntries int, maxExpandedBytes int64) error {
	if maxEntries < 1 || maxExpandedBytes < 1 {
		return errorsLimit("archive limits must be positive")
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	seen := make(map[string]struct{})
	var expanded int64
	for entries := 0; ; entries++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read module archive: %w", err)
		}
		if entries >= maxEntries {
			return errorsLimit("module archive has too many entries")
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		if name == cacheMarkerName {
			return fmt.Errorf("%w: reserved path %q", ErrUnsafeArchive, name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: duplicate path %q", ErrUnsafeArchive, name)
		}
		seen[name] = struct{}{}
		if header.Linkname != "" {
			return fmt.Errorf("%w: link target on %q", ErrUnsafeArchive, name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return fmt.Errorf("%w: directory %q has content", ErrUnsafeArchive, name)
			}
			if err := os.MkdirAll(filepath.Join(destination, filepath.FromSlash(name)), 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxExpandedBytes-expanded {
				return errorsLimit("module archive expands beyond the size limit")
			}
			expanded += header.Size
			target := filepath.Join(destination, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if header.FileInfo().Mode().Perm()&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return fmt.Errorf("create module archive path %q: %w", name, err)
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("extract module archive path %q: %w", name, copyErr)
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("%w: non-regular path %q", ErrUnsafeArchive, name)
		}
	}
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) {
		return "", fmt.Errorf("%w: path %q", ErrUnsafeArchive, name)
	}
	cleaned := path.Clean(strings.TrimSuffix(name, "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != strings.TrimSuffix(name, "/") {
		return "", fmt.Errorf("%w: path %q", ErrUnsafeArchive, name)
	}
	if len(cleaned) >= 2 && cleaned[1] == ':' {
		return "", fmt.Errorf("%w: path %q", ErrUnsafeArchive, name)
	}
	return cleaned, nil
}

func errorsLimit(message string) error {
	return fmt.Errorf("module archive limit: %s", message)
}
