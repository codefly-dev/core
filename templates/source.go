package templates

import (
	"github.com/codefly-dev/core/shared"
	"os"
)

type Source interface {
	ReadDir(dir shared.Dir) ([]os.DirEntry, error)
	ReadFile(file shared.File) ([]byte, error)
	Next() Source
}
