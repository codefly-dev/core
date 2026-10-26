// Command companionbump refuses a change to a companion's image inputs that
// keeps the companion's version.
//
//	go run ./internal/companionbump <base-ref>
//
// A companion's tag is its info.codefly.yaml version, and publishing skips a
// tag the registry already has. A change that keeps the version therefore
// either never ships or, forced, swaps the image under a tag machines have
// already cached, and those machines keep the old one. Run from the
// repository root of a checkout with <base-ref>'s history. A companion
// whose inputs (BuildSpec.Inputs) changed since its merge base with
// <base-ref> must carry a version above the one at <base-ref>. Taking the
// base's tip rather than the merge base also refuses a version that another
// change on the base already took.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/companions"
	"gopkg.in/yaml.v3"
)

// companion is what the check needs to know about one companion.
type companion struct {
	name    string
	version string   // at HEAD
	inputs  []string // repository-root-relative files and directories
}

// unbumped returns one refusal for every companion with a changed input whose
// version is not above its version at the base. base returns the version the
// base declares, and false when the companion does not exist there yet.
func unbumped(all []companion, changed []string, base func(name string) (string, bool, error)) ([]string, error) {
	var refusals []string
	for _, c := range all {
		touched := touchedInputs(c.inputs, changed)
		if len(touched) == 0 {
			continue
		}
		was, existed, err := base(c.name)
		if err != nil {
			return nil, err
		}
		if !existed {
			continue
		}
		now, err := semver.NewVersion(c.version)
		if err != nil {
			return nil, fmt.Errorf("companion <%s>: %w", c.name, err)
		}
		before, err := semver.NewVersion(was)
		if err != nil {
			return nil, fmt.Errorf("companion <%s> at the base: %w", c.name, err)
		}
		if now.GreaterThan(before) {
			continue
		}
		refusals = append(refusals, fmt.Sprintf(
			"companion <%s> changes its image (%s) but companions/%s/info.codefly.yaml says %s, and the base says %s: bump it above %s",
			c.name, strings.Join(touched, ", "), c.name, c.version, was, was))
	}
	return refusals, nil
}

// touchedInputs returns the changed files that are an input or lie under one.
func touchedInputs(inputs, changed []string) []string {
	var touched []string
	for _, file := range changed {
		for _, input := range inputs {
			if file == input || strings.HasPrefix(file, input+"/") {
				touched = append(touched, file)
				break
			}
		}
	}
	return touched
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output() //nolint:gosec // fixed git subcommands; the one variable is the base ref the workflow passes
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// changedFiles lists every path that differs between baseRef's merge base and
// HEAD. --no-renames lists a moved file at both of its paths: moving a file out
// of an input changes the image as surely as editing it. -z keeps each path as
// it is: without it git quotes a path with a space or a non-ASCII byte, and the
// quoted spelling matches no input.
func changedFiles(baseRef string) ([]string, error) {
	diff, err := git("diff", "--name-only", "--no-renames", "-z", baseRef+"...HEAD")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, file := range strings.Split(diff, "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	return files, nil
}

func run(baseRef string) ([]string, error) {
	specs, err := companions.BuildSpecs()
	if err != nil {
		return nil, err
	}
	all := make([]companion, 0, len(specs))
	for _, spec := range specs {
		inputs, inputsErr := spec.Inputs(".")
		if inputsErr != nil {
			return nil, inputsErr
		}
		all = append(all, companion{name: spec.Name, version: spec.Version, inputs: inputs})
	}
	changed, err := changedFiles(baseRef)
	if err != nil {
		return nil, err
	}
	base := func(name string) (string, bool, error) {
		manifest := "companions/" + name + "/info.codefly.yaml"
		if _, err := git("cat-file", "-e", baseRef+":"+manifest); err != nil {
			return "", false, nil
		}
		content, err := git("show", baseRef+":"+manifest)
		if err != nil {
			return "", false, err
		}
		var info struct {
			Version string `yaml:"version"`
		}
		if err := yaml.Unmarshal([]byte(content), &info); err != nil {
			return "", false, fmt.Errorf("%s at %s: %w", manifest, baseRef, err)
		}
		return info.Version, true, nil
	}
	return unbumped(all, changed, base)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: companionbump <base-ref>")
		os.Exit(2)
	}
	refusals, err := run(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, refusal := range refusals {
		fmt.Fprintln(os.Stderr, refusal)
	}
	if len(refusals) > 0 {
		os.Exit(1)
	}
	fmt.Println("every companion whose image changed carries a new version")
}
