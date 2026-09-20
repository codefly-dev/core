package proto

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

const rustOutputManifest = ".codefly-rust-output.json"

func generateRustOutput(ctx context.Context, destination string, generate func(string) error) error {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	destination, err = filepath.EvalSymlinks(destination)
	if err != nil {
		return err
	}
	lock := flock.New(destination+".rust.lock", flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return ctx.Err()
	}
	defer lock.Close()
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".rust-output-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := generate(stage); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishRustOutput(destination, stage)
}

func publishRustOutput(destination, stage string) error {
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	previous := map[string]string{}
	data, err := root.ReadFile(rustOutputManifest)
	if err == nil {
		if err := json.Unmarshal(data, &previous); err != nil {
			return fmt.Errorf("read Rust output ownership: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	next := map[string]string{}
	if err := filepath.WalkDir(stage, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular Rust output: %s", name)
		}
		rel, err := filepath.Rel(stage, name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		next[filepath.ToSlash(rel)] = rustOutputDigest(data)
		return nil
	}); err != nil {
		return err
	}
	paths := map[string]bool{}
	for name := range previous {
		paths[name] = true
	}
	for name := range next {
		paths[name] = true
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		if !fs.ValidPath(name) || name == "." || name == rustOutputManifest {
			return fmt.Errorf("invalid Rust output path: %q", name)
		}
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			names = append(names, name)
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Rust output conflicts with non-regular file: %s", name)
		}
		data, err := root.ReadFile(name)
		if err != nil {
			return err
		}
		if previous[name] == "" || rustOutputDigest(data) != previous[name] {
			return fmt.Errorf("Rust output conflicts with unowned or edited file: %s", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	data, err = json.Marshal(next)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, rustOutputManifest), data, 0o600); err != nil {
		return err
	}
	names = append(names, rustOutputManifest)
	backup, err := os.MkdirTemp(destination, ".rust-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(backup)
	var restore []func() error
	rollback := func(cause error) error {
		for i := len(restore) - 1; i >= 0; i-- {
			cause = errors.Join(cause, restore[i]())
		}
		return cause
	}
	for index, name := range names {
		old := filepath.Join(filepath.Base(backup), fmt.Sprint(index))
		if err := root.Rename(name, old); err == nil {
			restore = append(restore, func() error { return root.Rename(old, name) })
		} else if !errors.Is(err, fs.ErrNotExist) {
			return rollback(err)
		}
		if _, exists := next[name]; !exists && name != rustOutputManifest {
			continue
		}
		if err := root.MkdirAll(filepath.Dir(name), 0o750); err != nil {
			return rollback(err)
		}
		// Stage and destination share a filesystem; root confines publication even
		// when a destination ancestor is a symlink.
		staged := filepath.Join(filepath.Base(backup), "next")
		if err := os.Rename(filepath.Join(stage, filepath.FromSlash(name)), filepath.Join(destination, staged)); err != nil {
			return rollback(err)
		}
		if err := root.Rename(staged, name); err != nil {
			return rollback(err)
		}
		restore = append(restore, func() error { return root.Remove(name) })
	}
	return os.RemoveAll(backup)
}

func rustOutputDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
