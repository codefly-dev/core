package templates

import (
	"os"

	"github.com/codefly-dev/core/shared"
)

type Source interface {
	ReadDir(dir shared.Dir) ([]os.DirEntry, error)
	ReadFile(file shared.File) ([]byte, error)
	Next() Source
}
