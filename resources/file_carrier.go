package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A configuration value reaches a process as an environment variable set at
// process start. When the value is too large for that carrier it is delivered
// as a file instead, and the environment carries only the file's path under
// FileCarrierKey(key). See docs/runnable-binding-delivery.md.
const (
	// FileCarrierPrefix begins the environment key that carries the path of a
	// file-delivered value. The rest of the key is the value's own key without
	// its "CODEFLY__" prefix.
	FileCarrierPrefix = "CODEFLY__FILE__"

	// FileCarrierThreshold is the largest value delivered inline: a larger one
	// is delivered by file, by every emitter, automatically.
	FileCarrierThreshold = 32 << 10

	// MaxEnvironmentStringBytes is Linux's MAX_ARG_STRLEN: execve refuses an
	// environment whose any one "KEY=VALUE" string, with its terminating NUL,
	// is longer (E2BIG).
	MaxEnvironmentStringBytes = 128 << 10

	// MaxCarriedEnvironmentBytes bounds the environment Codefly itself emits
	// for one process. macOS caps the environment plus arguments at about
	// 1 MiB; Codefly keeps half of it for the parent environment and the
	// arguments, so a process it plans always starts.
	MaxCarriedEnvironmentBytes = 512 << 10

	// MaxFileCarrierBytes bounds one file-delivered value as a reader loads it.
	MaxFileCarrierBytes = 16 << 20

	// KubernetesFileCarrierMount is where a rendered workload's public
	// file-delivered values are mounted.
	KubernetesFileCarrierMount = "/var/run/codefly/configuration"
	// KubernetesSecretFileCarrierMount is where a rendered workload's secret
	// file-delivered values are mounted.
	KubernetesSecretFileCarrierMount = "/var/run/codefly/secret-configuration"
	// ContainerFileCarrierMount is where a locally run container sees its
	// file-delivered values.
	ContainerFileCarrierMount = "/var/run/codefly/configuration"
)

// ErrEnvironmentLimit refuses a process environment a platform would refuse
// at exec, before the process is started.
var ErrEnvironmentLimit = errors.New("process environment exceeds its limit")

// ErrFileCarrier refuses a file carrier that cannot be read as the value it
// carries.
var ErrFileCarrier = errors.New("file-delivered configuration value")

// fileCarrierName is what a delivered file may be named: a Kubernetes
// ConfigMap/Secret data key, which an environment key already is.
var fileCarrierName = regexp.MustCompile(`^[-._a-zA-Z0-9]{1,253}$`)

// FileCarrierKey is the environment key carrying the path of key's file.
func FileCarrierKey(key string) string {
	return FileCarrierPrefix + strings.TrimPrefix(key, "CODEFLY__")
}

// IsFileCarrierKey reports whether key carries a file path.
func IsFileCarrierKey(key string) bool {
	return strings.HasPrefix(key, FileCarrierPrefix)
}

// CarriedKey is the value key a file carrier stands for.
func CarriedKey(fileCarrierKey string) string {
	return "CODEFLY__" + strings.TrimPrefix(fileCarrierKey, FileCarrierPrefix)
}

// IsSecretCarrierKey reports whether a value key is in a secret namespace.
func IsSecretCarrierKey(key string) bool {
	for _, prefix := range secretCarrierPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// deliverByFile marks a configuration value too large to be delivered inline.
func deliverByFile(env *EnvironmentVariable, secret bool) *EnvironmentVariable {
	env.Secret = secret
	env.File = len(env.ValueAsString()) > FileCarrierThreshold
	return env
}

// MaterializeFileCarriers writes every file-delivered variable of envs into
// dir and returns the environment a process starts with: inline variables
// unchanged, and for each file-delivered one its FileCarrierKey set to
// visibleDir joined with the file's name. visibleDir is dir as the process
// sees it — dir itself for a host process, the mount point for a container.
//
// dir is created 0700 when absent; files are written 0600 and named by the
// value key and a digest of the content, so a changed value is a different
// path and a process reading an unchanged one sees the same path. A
// file-delivered variable whose key cannot name a file is refused.
func MaterializeFileCarriers(dir, visibleDir string, envs []*EnvironmentVariable) ([]*EnvironmentVariable, error) {
	var out []*EnvironmentVariable
	created := false
	for _, env := range envs {
		if env == nil {
			continue
		}
		if !env.File {
			out = append(out, env)
			continue
		}
		if !fileCarrierName.MatchString(env.Key) {
			return nil, fmt.Errorf("%w: key %q cannot name a delivered file", ErrFileCarrier, env.Key)
		}
		if !created {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("%w: create %s: %v", ErrFileCarrier, dir, err)
			}
			if err := os.Chmod(dir, 0o700); err != nil {
				return nil, fmt.Errorf("%w: restrict %s: %v", ErrFileCarrier, dir, err)
			}
			created = true
		}
		content := []byte(env.ValueAsString())
		sum := sha256.Sum256(content)
		name := env.Key + "." + hex.EncodeToString(sum[:8])
		if err := writePrivateFile(filepath.Join(dir, name), content); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrFileCarrier, env.Key, err)
		}
		out = append(out, &EnvironmentVariable{Key: FileCarrierKey(env.Key), Value: filepath.ToSlash(filepath.Join(visibleDir, name)), Secret: env.Secret})
	}
	return out, nil
}

// writePrivateFile replaces path with content, readable by its owner only.
// The content is written to a sibling first and renamed, so a reader never
// observes a partial value.
func writePrivateFile(path string, content []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".carrier-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(content)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// CheckProcessEnvironment refuses an environment a process could not start
// with: a file-delivered variable not yet materialized, any one "KEY=VALUE"
// string of MaxEnvironmentStringBytes or more (with its NUL), or Codefly
// variables together above MaxCarriedEnvironmentBytes. The error names the
// offending keys and their sizes, never a value.
func CheckProcessEnvironment(envs []*EnvironmentVariable) error {
	type sized struct {
		key  string
		size int
	}
	var all []sized
	total := 0
	for _, env := range envs {
		if env == nil {
			continue
		}
		if env.File {
			return fmt.Errorf("%w: %s is delivered by file and was not materialized", ErrEnvironmentLimit, env.Key)
		}
		size := len(env.Key) + 1 + len(env.ValueAsString()) + 1
		if size >= MaxEnvironmentStringBytes {
			return fmt.Errorf("%w: %s is %d bytes; one environment string must stay under %d (Linux MAX_ARG_STRLEN)", ErrEnvironmentLimit, env.Key, size, MaxEnvironmentStringBytes)
		}
		total += size
		all = append(all, sized{env.Key, size})
	}
	if total > MaxCarriedEnvironmentBytes {
		sort.Slice(all, func(i, j int) bool { return all[i].size > all[j].size })
		var largest []string
		for i := 0; i < len(all) && i < 5; i++ {
			largest = append(largest, fmt.Sprintf("%s (%d bytes)", all[i].key, all[i].size))
		}
		return fmt.Errorf("%w: Codefly environment is %d bytes, above %d; largest: %s", ErrEnvironmentLimit, total, MaxCarriedEnvironmentBytes, strings.Join(largest, ", "))
	}
	return nil
}

// ResolveFileCarriers reads every file carrier of environ ("KEY=VALUE"
// strings, as os.Environ returns them) and returns the value key of each with
// the content of its file. A carrier whose value key is also set inline, a
// relative path, a file that is not a regular file, is larger than
// MaxFileCarrierBytes or cannot be read, and a secret file accessible by
// others, are errors: an unreadable value never reads as an unset one.
func ResolveFileCarriers(environ []string) (map[string]string, error) {
	inline := map[string]bool{}
	carriers := map[string]string{}
	for _, entry := range environ {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if IsFileCarrierKey(key) {
			carriers[key] = value
			continue
		}
		if value != "" {
			inline[key] = true
		}
	}
	resolved := make(map[string]string, len(carriers))
	for carrier, path := range carriers {
		key := CarriedKey(carrier)
		if inline[key] {
			return nil, fmt.Errorf("%w: %s is delivered both inline and by file", ErrFileCarrier, key)
		}
		content, err := ReadFileCarrier(key, path)
		if err != nil {
			return nil, err
		}
		resolved[key] = content
	}
	return resolved, nil
}

// ReadFileCarrier reads the value key's file at path under the rules
// ResolveFileCarriers states.
func ReadFileCarrier(key, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %s: path %q is not absolute", ErrFileCarrier, key, path)
	}
	// Stat follows links: a Kubernetes volume presents each key as a link
	// into its current data directory.
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrFileCarrier, key, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s: %s is not a regular file", ErrFileCarrier, key, path)
	}
	if info.Size() > MaxFileCarrierBytes {
		return "", fmt.Errorf("%w: %s: %d bytes exceed %d", ErrFileCarrier, key, info.Size(), MaxFileCarrierBytes)
	}
	if IsSecretCarrierKey(key) && info.Mode().Perm()&0o007 != 0 {
		return "", fmt.Errorf("%w: %s: a secret file must not be accessible by others (mode %v)", ErrFileCarrier, key, info.Mode().Perm())
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrFileCarrier, key, err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, MaxFileCarrierBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrFileCarrier, key, err)
	}
	if len(content) > MaxFileCarrierBytes {
		return "", fmt.Errorf("%w: %s: exceeds %d bytes", ErrFileCarrier, key, MaxFileCarrierBytes)
	}
	return string(content), nil
}
