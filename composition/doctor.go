package composition

import (
	"context"
	"errors"
	"fmt"
	"os"
)

type DoctorCheck struct {
	Name   string           `json:"name"`
	Status ValidationStatus `json:"status"`
	Detail string           `json:"detail,omitempty"`
}

func (engine *Engine) Doctor(ctx context.Context, moduleDir string, ci bool) ([]DoctorCheck, error) {
	var checks []DoctorCheck
	if err := ctx.Err(); err != nil {
		return checks, err
	}
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return append(checks, DoctorCheck{Name: "descriptor", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "descriptor", Status: ValidationPassed})
	if _, err := LoadContributionInputs(moduleDir, descriptor); err != nil {
		return append(checks, DoctorCheck{Name: "contributions-schema", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "contributions-schema", Status: ValidationPassed})
	lock, err := LoadLock(moduleDir)
	if err != nil {
		return append(checks, DoctorCheck{Name: "lock", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "lock", Status: ValidationPassed})
	if lock.Module != descriptor.Name || lock.Package != descriptor.Base.ID {
		err = ErrPackageIdentity
		return append(checks, DoctorCheck{Name: "identity", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "identity", Status: ValidationPassed})
	digest, err := CompositionDigest(moduleDir, descriptor)
	if err != nil || digest != lock.CompositionDigest {
		if err == nil {
			err = fmt.Errorf("composition digest mismatch: got %s, want %s", digest, lock.CompositionDigest)
		}
		return append(checks, DoctorCheck{Name: "contributions", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "contributions", Status: ValidationPassed})
	cache, err := engine.materializer().Cached(lock)
	if err != nil {
		return append(checks, DoctorCheck{Name: "verified-cache", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "verified-cache", Status: ValidationPassed})
	manifest, err := LoadPackageManifest(cache)
	if err == nil {
		var toolVersion string
		toolVersion, err = engine.toolVersion(ctx)
		if err == nil {
			err = ValidateLockedContracts(descriptor, manifest, lock, toolVersion, engine.supportedContracts())
		}
	}
	if err != nil {
		return append(checks, DoctorCheck{Name: "contracts", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "contracts", Status: ValidationPassed})
	namespace, err := ResolveNamespace(engine.ProjectRoot, moduleDir, "stable", "", lock)
	if err != nil || !projectionMatches(namespace.ProjectionDir, lock) {
		if err == nil {
			err = errors.New("composed projection is missing or does not match its content digest")
		}
		return append(checks, DoctorCheck{Name: "projection", Status: ValidationFailed, Detail: err.Error()}), err
	}
	checks = append(checks, DoctorCheck{Name: "projection", Status: ValidationPassed})
	if !ci {
		override, overrideErr := LoadDevelopOverride(engine.ProjectRoot, descriptor.Name)
		if overrideErr == nil {
			if _, err := validateDevelopSource(override, descriptor, lock); err != nil {
				return append(checks, DoctorCheck{Name: "develop-override", Status: ValidationFailed, Detail: err.Error()}), err
			}
			checks = append(checks, DoctorCheck{Name: "develop-override", Status: ValidationPassed})
		} else if !errors.Is(overrideErr, os.ErrNotExist) {
			return append(checks, DoctorCheck{Name: "develop-override", Status: ValidationFailed, Detail: overrideErr.Error()}), overrideErr
		}
	}
	return checks, nil
}
