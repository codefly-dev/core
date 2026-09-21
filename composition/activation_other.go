//go:build !darwin && !linux

package composition

import "os"

func activateProjectionLink(candidate, destination string) error {
	return os.Rename(candidate, destination)
}
