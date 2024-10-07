package languages

type Language string

var (
	GO           Language = "go"
	PYTHON       Language = "python"
	NotSupported Language = "not-supported"
)

func FromString(s string) Language {
	switch s {
	case "go":
		return GO
	case "python":
		return PYTHON
	default:
		return NotSupported
	}
}
