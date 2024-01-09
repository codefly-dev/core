package builders

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/shared"
)

type Dependency struct {
	Components []string
	Ignore     shared.Ignore
}

func (dep *Dependency) Hash() (string, error) {
	return Hash(dep.Components, dep.Ignore)
}

func Hash(ps []string, ignore shared.Ignore) (string, error) {
	h := sha256.New()
	for _, path := range ps {
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// Skip directories
				return nil
			}
			if ignore != nil && ignore.Skip(path) {
				return nil
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			if _, err := io.Copy(h, file); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
