// Package python is the Python-specific runner: build, test, and run
// Python services across native, Docker, and Nix backends.
//
// It handles virtualenv / Poetry resolution, dependency installation,
// pytest invocation with filter wiring, and the agent-side helpers used
// by the python and python-fastapi agents.
package python
