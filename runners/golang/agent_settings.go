package golang

import (
	"fmt"
	"path/filepath"
	"strings"
)

// GoAgentSettings holds settings common to all Go service agents.
// Agent-specific settings (e.g. RestEndpoint for go-grpc) are defined
// in each agent and embed this struct.
type GoAgentSettings struct {
	HotReload                 bool   `yaml:"hot-reload"`
	DebugSymbols              bool   `yaml:"debug-symbols"`
	RaceConditionDetectionRun bool   `yaml:"race-condition-detection-run"`
	WithCGO                   bool   `yaml:"with-cgo"`
	WithWorkspace             bool   `yaml:"with-workspace"`
	SourceDir                 string `yaml:"source-dir"`
}

// GoSourceDir returns the configured source directory, defaulting to "code".
func (s *GoAgentSettings) GoSourceDir() string {
	if s == nil {
		return "code"
	}
	if s.SourceDir != "" {
		return s.SourceDir
	}
	return "code"
}

// Validate rejects source roots that can escape the service directory. The
// value is loaded from service.codefly.yaml and is later passed to directory
// creation, tool execution, Docker mounts, and destroy paths, so validation
// belongs at the shared settings boundary rather than in one runtime backend.
func (s *GoAgentSettings) Validate() error {
	dir := s.GoSourceDir()
	if !filepath.IsLocal(dir) || strings.ContainsAny(dir, "\x00\\") {
		return fmt.Errorf("go source-dir %q must stay within the service directory", dir)
	}
	return nil
}

// Setting name constants shared by all Go agents.
const (
	SettingHotReload                 = "hot-reload"
	SettingDebugSymbols              = "debug-symbols"
	SettingRaceConditionDetectionRun = "race-condition-detection-run"
)
