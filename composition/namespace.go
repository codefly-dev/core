package composition

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
)

type Namespace struct {
	Name             string
	LockPath         string
	Digest           string
	ProjectionDir    string
	CacheDir         string
	BuildDir         string
	NextJSDir        string
	RuntimeConfigDir string
	ContainerSuffix  string
	PortSeed         uint32
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
