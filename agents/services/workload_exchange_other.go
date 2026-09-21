//go:build !darwin && !linux

package services

import "errors"

func exchangeWorkloadTree(_, _ string) error {
	return errors.New("atomic workload identity publication requires Linux or macOS")
}
