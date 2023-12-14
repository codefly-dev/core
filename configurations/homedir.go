package configurations

import (
	"os/user"

	"github.com/codefly-dev/core/shared"
)

func HomeDir() string {
	activeUser, err := user.Current()
	if err != nil {
		shared.ExitOnError(err, "cannot get active user")
	}
	return activeUser.HomeDir
}
