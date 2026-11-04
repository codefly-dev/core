package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

// defaultConfigPath is where a consumer declares its exemptions.
const defaultConfigPath = ".github/core-pin.yaml"

// module is one go.mod and what it says about core.
type goMod struct {
	// dir is the module's directory, repository-root-relative and slash
	// separated. "." for a go.mod at the root.
	dir string
	// file is the go.mod path, repository-root-relative, for annotations.
	file string
	// line is the line the core requirement or replacement sits on, so the
	// annotation lands on it in the Files view.
	line int
	// pin is the version in `require`, before any replacement.
	pin string
	// indirect reports whether core is an indirect requirement. It still
	// decides which core compiles in — minimum version selection takes the
	// maximum across the whole graph — so it is checked like any other pin.
	// Only the remedy differs, and the report says so.
	indirect bool
	// replacement is the version a `replace` redirects core to, when it
	// redirects to a version rather than a path.
	replacement string
	// replacedLocally reports a `replace` pointing at a filesystem path. The
	// pin is then not what builds, so the gate cannot answer from it.
	replacedLocally bool
	// replacementTarget is the path or module a replacement names, for the
	// report.
	replacementTarget string
	// parseDegraded holds why this go.mod could only be read loosely, in which
	// case a `replace` in it was NOT seen. Nil for every file that parsed.
	parseDegraded error
}

// effectivePin is the version that actually decides which core compiles in: a
// replacement wins over the requirement, which is the whole reason a
// replacement cannot be allowed to read as "up to date".
func (m goMod) effectivePin() string {
	if m.replacement != "" {
		return m.replacement
	}
	return m.pin
}

// skipDirectory names trees whose go.mod files belong to somebody else.
//
// `vendor` and `node_modules` are copies of a dependency's own manifest, and
// `.git` is not source. Deliberately NOT skipped: `testdata`. A fixture pinned
// to an old core is a real thing in this fleet — codefly-dev/cli has two, both
// at a v0.5.11 pseudo-version — and skipping it by convention would mean the
// gate silently has no opinion about a whole class of module. It is reported,
// and the repository exempts it by NAME with a reason, which is a decision
// somebody made rather than a path that happened to match.
func skipDirectory(name string) bool {
	switch name {
	case "vendor", "node_modules", ".git":
		return true
	}
	return false
}

// discover walks root for every go.mod and reads what each says about core.
//
// Every one of them, not the first: the obin-ai modules pin core in
// modules/<name>/services/<svc>/code/go.mod and nowhere near the root, and
// obin-ai/module-robin spans v0.5.11 to v0.19.11 across eleven of them. A gate
// reading one go.mod per repository is how the spread in
// codefly-dev/core#891's table came out narrower than the fleet's real one.
func discover(root string) ([]goMod, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("read repository root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	var found []goMod
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skipDirectory(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Name() != "go.mod" {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// Parse, not ParseLax. ParseLax reads `module`, `go`, `toolchain` and
		// `require` and SKIPS `replace` entirely -- it leaves File.Replace
		// empty rather than reporting it cannot read one. A go.mod replacing
		// core with a local path would therefore have been judged on its
		// `require` line and reported up to date, which is the exact accident
		// this gate is supposed to refuse. Caught by
		// TestALocalReplaceFailsRatherThanReadingThePin.
		relative = filepath.ToSlash(relative)
		parsed, parseErr := modfile.Parse(path, raw, nil)
		if parseErr == nil {
			found = append(found, read(parsed, relative))
			return nil
		}
		// A go.mod carrying a directive this binary's x/mod does not know yet
		// would otherwise turn a new Go release into a red gate everywhere.
		// Fall back to the requirements -- and say that the replacements were
		// not read, rather than claiming there were none.
		lax, laxErr := modfile.ParseLax(path, raw, nil)
		if laxErr != nil {
			return fmt.Errorf("parse %s: %w", relative, laxErr)
		}
		degraded := read(lax, relative)
		degraded.parseDegraded = parseErr
		found = append(found, degraded)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return found, nil
}

// read projects one parsed go.mod into a module.
//
// ParseLax rather than Parse: a consumer's go.mod may carry a directive this
// binary's x/mod does not know yet, and refusing to read the file at all would
// turn a newer Go release into a red gate everywhere. The requirements and
// replacements are what this needs, and Lax still reads both.
func read(parsed *modfile.File, relative string) goMod {
	found := goMod{
		dir:  pathDir(relative),
		file: relative,
	}
	for _, require := range parsed.Require {
		if require.Mod.Path != corePath {
			continue
		}
		found.pin = require.Mod.Version
		found.indirect = require.Indirect
		if require.Syntax != nil {
			found.line = require.Syntax.Start.Line
		}
	}
	// A replacement is read whether or not core is required here: `replace`
	// with no `require` is inert, and a module with neither is not a pin at
	// all, so only a replacement alongside a requirement reaches a verdict.
	for _, replace := range parsed.Replace {
		if replace.Old.Path != corePath {
			continue
		}
		// A replacement narrowed to one version does not apply to a pin at any
		// other version, so it is not what builds and not what to judge.
		if replace.Old.Version != "" && replace.Old.Version != found.pin {
			continue
		}
		found.replacementTarget = replace.New.Path
		if replace.New.Version == "" {
			// No version means a filesystem path: ../core, ./vendor/core.
			found.replacedLocally = true
		} else {
			found.replacement = replace.New.Version
			found.replacementTarget = replace.New.Path + "@" + replace.New.Version
		}
		if replace.Syntax != nil {
			found.line = replace.Syntax.Start.Line
		}
	}
	return found
}

// isFixturePath reports whether a module sits under a testdata directory.
//
// It changes the ADVICE, never the verdict. A fixture is still reported and
// still fails, because "skip anything under testdata" is the kind of silent
// convention that leaves a gate with no opinion about a whole class of module
// — and in this fleet a `testdata` tree is sometimes a real published module.
// The repository exempts it by name instead.
func isFixturePath(dir string) bool {
	for _, segment := range strings.Split(dir, "/") {
		if segment == "testdata" {
			return true
		}
	}
	return false
}

// pathDir returns the slash-separated directory of a repository-relative file.
func pathDir(relative string) string {
	dir := filepath.ToSlash(filepath.Dir(relative))
	if dir == "" {
		return "."
	}
	return dir
}

// exemption is one module a repository has decided not to hold to the
// threshold, and why.
type exemption struct {
	// Path is the module's DIRECTORY, repository-root-relative, or a prefix
	// ending in "/..." meaning that directory and everything under it.
	Path string `yaml:"path"`
	// Reason is required. An exemption without one is refused: the gate's
	// whole value is that somebody reading a red check knows what to change,
	// and an unexplained exemption is the same hole with the check removed.
	Reason string `yaml:"reason"`

	// matched records whether any module fell under this entry, so a stale
	// exemption can be refused rather than quietly covering nothing.
	matched bool
}

// config is the consumer's exemption file.
type config struct {
	Exempt []*exemption `yaml:"exempt"`
}

// loadConfig reads the exemption file. Its absence is normal and means no
// exemptions; a malformed one is an error, because a file whose entries were
// silently dropped is indistinguishable from a repository holding the line.
func loadConfig(root, relative string) (*config, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		if os.IsNotExist(err) {
			return &config{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", relative, err)
	}

	loaded := &config{}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(loaded); err != nil {
		return nil, fmt.Errorf("parse %s: %w", relative, err)
	}

	for index, entry := range loaded.Exempt {
		if strings.TrimSpace(entry.Path) == "" {
			return nil, fmt.Errorf("%s: exemption %d has no path", relative, index)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return nil, fmt.Errorf(
				"%s: the exemption for %q has no reason.\n"+
					"Every exemption states why, because a red check nobody can explain "+
					"and an exemption nobody can explain are the same hole",
				relative, entry.Path)
		}
		entry.Path = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(entry.Path)), "/")
	}
	return loaded, nil
}

// exempt reports whether a module is exempt, and under which entry.
func (c *config) exempt(dir string) (*exemption, bool) {
	for _, entry := range c.Exempt {
		if prefix, recursive := strings.CutSuffix(entry.Path, "/..."); recursive {
			if dir == prefix || strings.HasPrefix(dir, prefix+"/") {
				entry.matched = true
				return entry, true
			}
			continue
		}
		if dir == entry.Path {
			entry.matched = true
			return entry, true
		}
	}
	return nil, false
}

// unused returns the exemptions that covered nothing.
func (c *config) unused() []string {
	var stale []string
	for _, entry := range c.Exempt {
		if !entry.matched {
			stale = append(stale, entry.Path)
		}
	}
	return stale
}
