package configurations

import (
	"github.com/hygge-io/hygge/pkg/core"
	"os/user"
)

func HomeDir() string {
	currentUser, err := user.Current()
	if err != nil {
		core.ExitOnError(err, "cannot get current user")
	}
	return currentUser.HomeDir
}
