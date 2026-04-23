package languages

import "testing"

// FromString is the only public function and is the binding contract
// for service.codefly.yaml's `language:` field. Lock the mapping.
func TestFromString(t *testing.T) {
	cases := map[string]Language{
		"go":         GO,
		"python":     PYTHON,
		"typescript": TYPESCRIPT,
		"ts":         TYPESCRIPT, // alias
		"":           NotSupported,
		"rust":       NotSupported,
		"GO":         NotSupported, // case-sensitive — explicit
	}
	for in, want := range cases {
		if got := FromString(in); got != want {
			t.Errorf("FromString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLanguage_IsString(t *testing.T) {
	// Language is a string newtype. Round-tripping it through string
	// should be free.
	if string(GO) != "go" {
		t.Errorf("GO underlying value: %q", string(GO))
	}
	if string(NotSupported) != "not-supported" {
		t.Errorf("NotSupported underlying value: %q", string(NotSupported))
	}
}
