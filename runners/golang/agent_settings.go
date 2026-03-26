package golang

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
	if s.SourceDir != "" {
		return s.SourceDir
	}
	return "code"
}

// Setting name constants shared by all Go agents.
const (
	SettingHotReload                 = "hot-reload"
	SettingDebugSymbols              = "debug-symbols"
	SettingRaceConditionDetectionRun = "race-condition-detection-run"
)
