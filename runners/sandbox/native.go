package sandbox

import "os/exec"

// nativeSandbox is the no-op backend. All declarations are recorded
// but never enforced; Wrap returns the cmd unchanged.
//
// Used for tests and for callers that have explicitly authorized
// unrestricted exec — the callsite reads `sandbox.NewNative()`, which
// is auditable in code review.
type nativeSandbox struct {
	policy
}

func (s *nativeSandbox) WithReadPaths(paths ...string) Sandbox {
	s.readPaths = append(s.readPaths, paths...)
	return s
}

func (s *nativeSandbox) WithWritePaths(paths ...string) Sandbox {
	s.writePaths = append(s.writePaths, paths...)
	return s
}

func (s *nativeSandbox) WithNetwork(p NetworkPolicy) Sandbox {
	s.network = p
	return s
}

func (s *nativeSandbox) WithUnixSockets(paths ...string) Sandbox {
	s.unixSockets = append(s.unixSockets, paths...)
	return s
}

func (s *nativeSandbox) Wrap(_ *exec.Cmd) error { return nil }

func (s *nativeSandbox) Backend() Backend { return BackendNative }
