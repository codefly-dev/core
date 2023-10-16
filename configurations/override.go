package configurations

type Override interface {
	Override(p string) bool
}
