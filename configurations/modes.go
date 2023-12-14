package configurations

type Mode = string

var mode Mode

func InPartialMode() bool {
	return mode == ModePartial
}

func ApplicationMode() bool {
	return mode == ModeApplication
}

func SetMode(m Mode) {
	mode = m
}

const (
	ModeApplication Mode = "application"
	ModePartial     Mode = "partial"
	ModeService     Mode = "service"
)
