package rust

// RustAgentSettings holds settings common to all Rust service agents.
// Agent-specific settings are defined in each agent and embed this struct,
// mirroring core/runners/golang.GoAgentSettings.
type RustAgentSettings struct {
	HotReload    bool   `yaml:"hot-reload"`
	DebugSymbols bool   `yaml:"debug-symbols"`
	// Release builds with the optimized `--release` profile. Off by default
	// for a fast dev loop (debug profile compiles faster).
	Release bool `yaml:"release"`
	// Features are Cargo feature flags passed as `--features f1,f2`.
	Features  []string `yaml:"features"`
	SourceDir string   `yaml:"source-dir"`
}

// RustSourceDir returns the configured source directory, defaulting to "code".
func (s *RustAgentSettings) RustSourceDir() string {
	if s.SourceDir != "" {
		return s.SourceDir
	}
	return "code"
}

// Setting name constants shared by all Rust agents.
const (
	SettingHotReload    = "hot-reload"
	SettingDebugSymbols = "debug-symbols"
	SettingRelease      = "release"
)
