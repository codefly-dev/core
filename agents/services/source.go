package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/resources"
)

// ResolveSourceLocation binds Code and Tooling services to project source
// without requiring Runtime.Load. The agent manager places a real service
// declaration at WorkDirEnvironment; that declaration remains the settings
// authority for generated source attachments and ordinary services alike.
//
// ARCHITECTURE: read-only project inspection must not initialize a language
// runtime. This resolver loads only Codefly configuration, hydrates the
// caller's existing settings value, and follows an attachment symlink so Core
// VFS implementations inventory the physical project tree.
func (s *Base) ResolveSourceLocation(ctx context.Context, settings any, sourceDir func() string) (string, error) {
	if s != nil {
		s.loadMu.Lock()
		defer s.loadMu.Unlock()
	}

	root := strings.TrimSpace(os.Getenv(agents.WorkDirEnvironment))
	if root == "" && s != nil {
		root = s.Location
	}
	if root == "" {
		root = "."
	}

	declarationPath := filepath.Join(root, resources.ServiceConfigurationName)
	if info, err := os.Stat(declarationPath); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("service declaration %s is a directory", declarationPath)
		}
		declaration, loadErr := resources.LoadServiceFromDir(ctx, root)
		if loadErr != nil {
			return "", fmt.Errorf("load source service declaration: %w", loadErr)
		}
		if settings != nil {
			if loadErr := declaration.LoadSettingsFromSpec(settings); loadErr != nil {
				return "", fmt.Errorf("load source service settings: %w", loadErr)
			}
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect source service declaration: %w", err)
	}

	location := root
	if sourceDir != nil {
		configured := strings.TrimSpace(sourceDir())
		if configured != "" {
			configured = filepath.FromSlash(configured)
			if !filepath.IsLocal(configured) {
				return "", fmt.Errorf("source-dir %q must stay within the service directory", configured)
			}
			location = filepath.Join(root, configured)
		}
	}
	location = filepath.Clean(location)
	if physical, err := filepath.EvalSymlinks(location); err == nil {
		return physical, nil
	}
	return location, nil
}
