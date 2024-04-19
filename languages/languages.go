package languages

type Language string

var (
	GO           Language = "go"
	NotSupported Language = "not-supported"
)

func FromString(s string) Language {
	switch s {
	case "go":
		return GO
	default:
		return NotSupported
	}
}
