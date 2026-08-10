package languages

type Language string

var (
	GO           Language = "go"
	PYTHON       Language = "python"
	TYPESCRIPT   Language = "typescript"
	JAVASCRIPT   Language = "javascript"
	RUST         Language = "rust"
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
	case "javascript", "js":
		return JAVASCRIPT
	case "rust":
		return RUST
	default:
		return NotSupported
	}
}
