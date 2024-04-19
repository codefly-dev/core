package resources

type Mode = string

var mode Mode

func InPartialMode() bool {
	return mode == ModePartial
}

func ModuleMode() bool {
	return mode == ModeModule
}

func SetMode(m Mode) {
	mode = m
}

const (
	ModeModule  Mode = "module"
	ModePartial Mode = "partial"
	ModeService Mode = "service"
)
