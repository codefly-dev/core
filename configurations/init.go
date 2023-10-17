package configurations

import (
	"github.com/codefly-dev/core/shared"
	"os/user"
)

func HomeDir() string {
	currentUser, err := user.Current()
	if err != nil {
		shared.ExitOnError(err, "cannot get current user")
	}
	return currentUser.HomeDir
}
