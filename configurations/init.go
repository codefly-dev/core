package configurations

import (
	"os/user"

	"github.com/codefly-dev/core/shared"
)

func HomeDir() string {
	currentUser, err := user.Current()
	if err != nil {
		shared.ExitOnError(err, "cannot get current user")
	}
	return currentUser.HomeDir
}
