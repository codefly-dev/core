package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/codefly-dev/core/shared"
)

type DevelopOverride struct {
	Schema    string            `json:"schema"`
	Module    string            `json:"module"`
	Package   string            `json:"package"`
	Source    string            `json:"source"`
	Contracts map[string]string `json:"contracts"`
}

func SetDevelopOverride(ctx context.Context, projectRoot, moduleDir, source string) (*DevelopOverride, error) {
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return nil, err
	}
	lock, err := LoadLock(moduleDir)
	if err != nil {
		return nil, err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	manifest, err := LoadPackageManifest(source)
	if err != nil {
		return nil, err
	}
	if manifest.ID != descriptor.Base.ID || manifest.ID != lock.Package {
		return nil, ErrPackageIdentity
	}
	for contract, lockedVersion := range lock.Contracts {
		rangeValue, exists := manifest.Contracts[contract]
		if !exists {
			return nil, fmt.Errorf("%w: local source does not advertise %s", ErrContract, contract)
		}
		constraint, _ := semver.NewConstraint(rangeValue)
		version, _ := semver.NewVersion(lockedVersion)
		if !constraint.Check(version) {
			return nil, fmt.Errorf("%w: local source %s contract %s does not include locked version %s", ErrContract, contract, rangeValue, lockedVersion)
		}
	}
	override := &DevelopOverride{
		Schema: "codefly/module-develop/v2", Module: descriptor.Name, Package: manifest.ID,
		Source: source, Contracts: lock.Contracts,
	}
	data, err := json.MarshalIndent(override, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := ensureCodeflyIgnore(projectRoot); err != nil {
		return nil, err
	}
	if err := shared.WriteFileAtomic(ctx, developOverridePath(projectRoot, descriptor.Name), append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	return override, nil
}

func ClearDevelopOverride(projectRoot, module string) error {
	if err := validateIdentifier("module", module); err != nil {
		return err
	}
	err := os.Remove(developOverridePath(projectRoot, module))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func LoadDevelopOverride(projectRoot, module string) (*DevelopOverride, error) {
	data, err := os.ReadFile(developOverridePath(projectRoot, module))
	if err != nil {
		return nil, err
	}
	var override DevelopOverride
	if err := decodeStrictJSON(data, &override); err != nil {
		return nil, err
	}
	if override.Schema != "codefly/module-develop/v2" || override.Module != module || override.Package == "" || !filepath.IsAbs(override.Source) {
		return nil, errors.New("local module develop override is invalid")
	}
	return &override, nil
}

func developOverridePath(projectRoot, module string) string {
	return filepath.Join(projectRoot, DevelopOverrideDirectory, module+".json")
}

func ensureCodeflyIgnore(projectRoot string) error {
	directory := filepath.Join(projectRoot, ".codefly")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	path := filepath.Join(directory, ".gitignore")
	data, err := os.ReadFile(path)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "*" {
				return nil
			}
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
		return os.WriteFile(path, append(data, []byte("*\n")...), 0o644)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte("*\n"), 0o644)
}
