package shared

import "time"

// Retry attempts to execute a task function until it succeeds or reaches a maximum number of attempts.
func Retry(interval time.Duration, maxAttempts int, task func() error) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		if i > 0 {
			time.Sleep(interval)
		}
		err = task()
		if err == nil {
			return nil
		}
	}
	return err
}
