package resources

import (
	"context"
	"os/user"
	"path"

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
	w.Trace("checking if workspace is initialized")
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

func init() {
	codeflyDir = path.Join(shared.Must(HomeDir()), ".codefly")
}
