package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ResetTrivyDatabases removes Trivy's vulnerability and Java databases under
// the same cross-process lock used by Docker. It preserves the scan cache and
// lock file. When containerized Trivy created root-owned bind-mount contents,
// cleanup falls back to the pinned Trivy image so the files are removed with
// the same privileges that created them.
func ResetTrivyDatabases(ctx context.Context) (returnErr error) {
	cache, err := trivyCacheDir()
	if err != nil {
		return fmt.Errorf("resolve Trivy cache: %w", err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return fmt.Errorf("create Trivy cache: %w", err)
	}
	cacheLock, err := lockTrivyCache(ctx, cache)
	if err != nil {
		return err
	}
	defer func() {
		if err := cacheLock.Unlock(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("unlock Trivy cache after reset: %w", err))
		}
	}()

	removeErr := removeTrivyDatabaseDirectories(cache)
	if removeErr == nil {
		return verifyTrivyDatabasesRemoved(cache)
	}
	if !have("docker") {
		return errors.Join(removeErr, fmt.Errorf("containerized Trivy cleanup unavailable: docker executable not found"))
	}
	_, dockerErr := runCmd(ctx, "", "docker",
		"run", "--rm", "--network", "none",
		"-v", cache+":/root/.cache/trivy",
		TrivyImage, "clean", "--vuln-db", "--java-db",
	)
	if dockerErr != nil {
		return errors.Join(removeErr, fmt.Errorf("containerized Trivy cleanup failed: %w", dockerErr))
	}
	if err := verifyTrivyDatabasesRemoved(cache); err != nil {
		return errors.Join(removeErr, err)
	}
	return nil
}

func trivyCacheDir() (string, error) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userCache, "codefly", "trivy"), nil
}

func removeTrivyDatabaseDirectories(cache string) error {
	var errs error
	if err := os.RemoveAll(filepath.Join(cache, "db")); err != nil {
		errs = errors.Join(errs, fmt.Errorf("remove Trivy vulnerability database: %w", err))
	}
	if err := os.RemoveAll(filepath.Join(cache, "java-db")); err != nil {
		errs = errors.Join(errs, fmt.Errorf("remove Trivy Java database: %w", err))
	}
	return errs
}

func verifyTrivyDatabasesRemoved(cache string) error {
	var errs error
	for _, database := range []struct {
		name string
		path string
	}{
		{name: "vulnerability", path: filepath.Join(cache, "db")},
		{name: "Java", path: filepath.Join(cache, "java-db")},
	} {
		if _, err := os.Stat(database.path); err == nil {
			errs = errors.Join(errs, fmt.Errorf("Trivy %s database still exists after cleanup: %w", database.name, os.ErrExist))
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = errors.Join(errs, fmt.Errorf("verify Trivy %s database cleanup: %w", database.name, err))
		}
	}
	return errs
}
