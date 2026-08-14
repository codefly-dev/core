package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

type CommandSpec struct {
	Name      string
	Command   []string
	Directory string
	Env       []string
}

type CommandRunner interface {
	Run(context.Context, CommandSpec) error
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(ctx context.Context, spec CommandSpec) error {
	command := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	command.Dir = spec.Directory
	command.Env = append(os.Environ(), spec.Env...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s: %w", spec.Name, err)
	}
	return nil
}

type Renderer struct {
	Runner CommandRunner
}

type CompositionInput struct {
	Schema       string            `json:"schema"`
	Module       string            `json:"module"`
	Package      string            `json:"package"`
	Version      string            `json:"version"`
	ConsumerRoot string            `json:"consumerRoot"`
	Projection   string            `json:"projection"`
	Contracts    map[string]string `json:"contracts"`
	Descriptor   *Descriptor       `json:"descriptor"`
}

func (renderer Renderer) Render(ctx context.Context, base, moduleDir, projection string, descriptor *Descriptor, manifest *PackageManifest, contracts map[string]string) (*Catalog, []ValidationResult, error) {
	if renderer.Runner == nil {
		renderer.Runner = ExecCommandRunner{}
	}
	if err := copyProjection(base, projection); err != nil {
		return nil, nil, fmt.Errorf("render base projection: %w", err)
	}
	moduleDir, err := filepath.Abs(moduleDir)
	if err != nil {
		return nil, nil, err
	}
	projection, err = filepath.Abs(projection)
	if err != nil {
		return nil, nil, err
	}
	input := CompositionInput{
		Schema:       "codefly/composition-input/v2",
		Module:       descriptor.Name,
		Package:      manifest.ID,
		Version:      manifest.Version,
		ConsumerRoot: moduleDir,
		Projection:   projection,
		Contracts:    contracts,
		Descriptor:   descriptor,
	}
	inputData, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	inputPath := filepath.Join(projection, filepath.FromSlash(CompositionInputName))
	if err := os.MkdirAll(filepath.Dir(inputPath), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(inputPath, append(inputData, '\n'), 0o644); err != nil {
		return nil, nil, err
	}
	environment := []string{
		"CODEFLY_COMPOSITION_INPUT=" + inputPath,
		"CODEFLY_COMPOSITION_CONSUMER=" + moduleDir,
		"CODEFLY_COMPOSITION_PROJECTION=" + projection,
	}
	validations := make([]ValidationResult, 0, len(manifest.Generators)+len(manifest.Conformance)+len(descriptor.Contributions.Tests)+1)
	for _, generator := range manifest.Generators {
		if err := renderer.runPackageCommand(ctx, projection, generator, environment); err != nil {
			validations = append(validations, ValidationResult{Name: generator.Name, Kind: "generator", Status: ValidationFailed, Detail: err.Error()})
			return nil, validations, err
		}
		validations = append(validations, ValidationResult{Name: generator.Name, Kind: "generator", Status: ValidationPassed})
	}
	catalog, err := LoadCatalog(projection)
	if err != nil {
		validations = append(validations, ValidationResult{Name: "collisions", Kind: "validation", Status: ValidationFailed, Detail: err.Error()})
		return nil, validations, err
	}
	claims := append([]Claim(nil), manifest.Claims...)
	for index := range claims {
		claims[index].Owner = "base"
	}
	claims = append(claims, descriptorClaims(descriptor)...)
	claims = append(claims, catalog.Claims...)
	if err := ValidateCollisions(claims, manifest.ReservedNamespaces); err != nil {
		validations = append(validations, ValidationResult{Name: "collisions", Kind: "validation", Status: ValidationFailed, Detail: err.Error()})
		return nil, validations, err
	}
	validations = append(validations, ValidationResult{Name: "collisions", Kind: "validation", Status: ValidationPassed})
	for _, suite := range manifest.Conformance {
		if err := renderer.runPackageCommand(ctx, projection, suite, environment); err != nil {
			validations = append(validations, ValidationResult{Name: suite.Name, Kind: "conformance", Status: ValidationFailed, Detail: err.Error()})
			return nil, validations, err
		}
		validations = append(validations, ValidationResult{Name: suite.Name, Kind: "conformance", Status: ValidationPassed})
	}
	for _, suite := range descriptor.Contributions.Tests {
		directory := filepath.Join(moduleDir, filepath.FromSlash(suite.Path))
		spec := CommandSpec{Name: suite.Path, Command: suite.Command, Directory: directory, Env: environment}
		if err := renderer.Runner.Run(ctx, spec); err != nil {
			validations = append(validations, ValidationResult{Name: suite.Path, Kind: "integration", Status: ValidationFailed, Detail: err.Error()})
			return nil, validations, err
		}
		validations = append(validations, ValidationResult{Name: suite.Path, Kind: "integration", Status: ValidationPassed})
	}
	catalog.Claims = claims
	return catalog, validations, nil
}

func (renderer Renderer) runPackageCommand(ctx context.Context, projection string, command PackageCommand, environment []string) error {
	directory := projection
	if command.WorkingDirectory != "" {
		directory = filepath.Join(projection, filepath.FromSlash(command.WorkingDirectory))
	}
	return renderer.Runner.Run(ctx, CommandSpec{Name: command.Name, Command: command.Command, Directory: directory, Env: environment})
}

func copyProjection(source, destination string) error {
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		if relative == cacheMarkerName {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return fmt.Errorf("base cache contains unsafe path %q", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		input, err := os.Open(current)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		outputCloseErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return outputCloseErr
	})
}
