package python

// PythonAgentSettings holds settings common to all Python service agents.
type PythonAgentSettings struct {
	PythonVersion string `yaml:"python-version"`
	SourceDir     string `yaml:"source-dir"`
}

// PythonSourceDir returns the configured source directory.
// Python convention: source is at the service root (not a subdirectory).
func (s *PythonAgentSettings) PythonSourceDir() string {
	if s.SourceDir != "" {
		return s.SourceDir
	}
	return "."
}
