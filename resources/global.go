package resources

import (
	"context"
	"os"
	"os/user"
	"path"
	"path/filepath"

	wool "github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"
)

func HomeDir() (string, error) {
	activeUser, err := user.Current()
	if err != nil {
		return "", err
	}
	return activeUser.HomeDir, nil
}

func Init(ctx context.Context) (bool, error) {
	w := wool.Get(ctx)
	w.Trace("checking if codefly is initialized")
	return shared.CheckEmptyDirectoryOrCreate(ctx, CodeflyDir())
}

/*
Global
*/

// CodeflyDir returns the directory where the Workspace configuration is stored
func CodeflyDir() string {
	return codeflyDir
}

var (
	codeflyDir string
	// This is where the Workspace configuration is stored
	// default to ~/.codefly
)

func FindConfigDir() (*string, error) {
	cur, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	var atRoot bool
	for {
		p := path.Join(cur, ".codefly")
		exists, err := shared.DirectoryExists(context.Background(), p)
		if err != nil {
			return nil, err
		}
		if exists {
			return &p, nil
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			if atRoot {
				return nil, nil
			}
			atRoot = true
		}
	}
}

func init() {
	found := shared.Must(FindConfigDir())
	if found != nil {
		codeflyDir = *found
		return
	}
	// Or use current path
	cur, err := os.Getwd()
	if err != nil {
		codeflyDir = ".codefly"
		return
	}
	codeflyDir = path.Join(cur, ".codefly")
}
