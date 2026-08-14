package composition

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Namespace struct {
	Name             string `json:"name"`
	LockPath         string `json:"lockPath"`
	Digest           string `json:"digest"`
	ProjectionDir    string `json:"projectionDir"`
	CacheDir         string `json:"cacheDir"`
	BuildDir         string `json:"buildDir"`
	NextJSDir        string `json:"nextJSDir"`
	RuntimeConfigDir string `json:"runtimeConfigDir"`
	ContainerSuffix  string `json:"containerSuffix"`
	PortSeed         uint32 `json:"portSeed"`
}

func ResolveNamespace(projectRoot, moduleDir, name, lockPath string, lock *Lock) (*Namespace, error) {
	if err := validateIdentifier("composition namespace", name); err != nil {
		return nil, err
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	if lockPath == "" {
		lockPath = filepath.Join(moduleDir, LockFileName)
	}
	value := strings.Join([]string{name, lock.Artifact.Digest, lock.CompositionDigest, lock.Source.Commit}, "\x00")
	digestBytes := sha256.Sum256([]byte(value))
	digest := fmt.Sprintf("sha256:%x", digestBytes)
	leaf := fmt.Sprintf("%x", digestBytes)
	root := filepath.Join(projectRoot, ".codefly", "namespaces", name, lock.Module, leaf)
	return &Namespace{
		Name:             name,
		LockPath:         lockPath,
		Digest:           digest,
		ProjectionDir:    filepath.Join(projectRoot, ".codefly", "composed", name, lock.Module, leaf),
		CacheDir:         filepath.Join(root, "cache"),
		BuildDir:         filepath.Join(root, "build"),
		NextJSDir:        filepath.Join(root, "build", "next"),
		RuntimeConfigDir: filepath.Join(root, "runtime"),
		ContainerSuffix:  name + "-" + leaf[:12],
		PortSeed:         binary.BigEndian.Uint32(digestBytes[:4]),
	}, nil
}

func (namespace *Namespace) Prepare() error {
	for _, directory := range []string{namespace.CacheDir, namespace.BuildDir, namespace.NextJSDir, namespace.RuntimeConfigDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	return nil
}
