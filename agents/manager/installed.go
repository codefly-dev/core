package manager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blang/semver"
	"github.com/codefly-dev/core/resources"
)

// Installed returns the newest installed executable for each publisher/name.
// Installation and release order say nothing about protocol compatibility:
// callers must inspect the selected process before using its capabilities.
func Installed(ctx context.Context, kind resources.AgentKind) ([]*resources.Agent, error) {
	return InstalledAt(ctx, resources.AgentBase(ctx), kind)
}

// InstalledAt reads another Codefly home's installed artifacts without changing
// the process environment. This is used to seed isolated agent qualification.
func InstalledAt(ctx context.Context, home string, kind resources.AgentKind) ([]*resources.Agent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	registration, err := resources.AgentKindRegistrationFor(kind)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, "agents", registration.InstallSubdirectory)
	publishers, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list installed agents: %w", err)
	}
	var result []*resources.Agent
	for _, publisher := range publishers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !publisher.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, publisher.Name()))
		if err != nil {
			return nil, fmt.Errorf("list installed agents for %s: %w", publisher.Name(), err)
		}
		latest := make(map[string]semver.Version)
		for _, entry := range entries {
			name, version, ok := strings.Cut(entry.Name(), "__")
			if !ok || name == "" {
				continue
			}
			parsed, err := semver.Parse(version)
			if err != nil {
				continue
			}
			info, err := os.Stat(filepath.Join(root, publisher.Name(), entry.Name()))
			if err != nil {
				return nil, fmt.Errorf("inspect installed agent %s: %w", entry.Name(), err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				continue
			}
			if previous, exists := latest[name]; !exists || parsed.GT(previous) {
				latest[name] = parsed
			}
		}
		for name, version := range latest {
			result = append(result, &resources.Agent{
				Kind: kind, Publisher: publisher.Name(), Name: name, Version: version.String(),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Identifier() < result[j].Identifier() })
	return result, nil
}
