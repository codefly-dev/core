package languages

type Language string

var (
	GO           Language = "go"
	PYTHON       Language = "python"
	TYPESCRIPT   Language = "typescript"
	NotSupported Language = "not-supported"
)

func FromString(s string) Language {
	switch s {
	case "go":
		return GO
	case "python":
		return PYTHON
	case "typescript", "ts":
		return TYPESCRIPT
	default:
		return NotSupported
	}
}
