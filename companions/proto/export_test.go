package proto

import "github.com/codefly-dev/core/runners/companion"

// PinClientCompanionBackend pins the backend client generation runs on and
// returns a function restoring the previous choice.
func PinClientCompanionBackend(backend companion.Backend) func() {
	previous := clientCompanionBackend
	clientCompanionBackend = backend
	return func() { clientCompanionBackend = previous }
}
