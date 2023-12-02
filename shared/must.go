package shared

func Must[T any](t T, err error) T {
	if err != nil {
		ExitOnError(err, "cannot cast")
	}
	return t
}
