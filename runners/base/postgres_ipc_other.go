//go:build !darwin

package base

import "context"

// ReapOrphanedPostgresIPC is a no-op on platforms where Codefly's PostgreSQL
// runtime does not use Darwin System V IPC resources.
func ReapOrphanedPostgresIPC(context.Context) error { return nil }
