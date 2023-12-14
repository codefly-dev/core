package shared

func DefaultTo(s string, defaultValue string) string {
	if s == "" {
		return defaultValue
	}
	return s
}
