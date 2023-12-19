package configurations

import (
	"os/user"
)

func HomeDir() (string, error) {
	activeUser, err := user.Current()
	if err != nil {
		return "", err
	}
	return activeUser.HomeDir, nil
}
