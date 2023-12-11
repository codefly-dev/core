package shared

func Pointer[T any](t T) *T {
	return &t
}

// PointerEqual returns true if pointer is not nil and value is equal
func PointerEqual[T comparable](t *T, target T) bool {
	return t != nil && *t == target
}
