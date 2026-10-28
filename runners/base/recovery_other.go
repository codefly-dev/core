//go:build !darwin

package base

func recoveryLegacyBootMatches(string) (bool, error) { return false, nil }
