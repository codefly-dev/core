package wool_test

import (
	"testing"

	"github.com/codefly-dev/core/wool"
)

func TestSanitizeForward(t *testing.T) {
	tcs := []struct {
		header string
		wanted string
	}{
		{"User-Agent", "wool:user-agent"},
		{"X-Forwarded-For", "wool:forwarded-for"},
	}
	for _, tc := range tcs {
		t.Run(tc.header, func(t *testing.T) {
			got := wool.HeaderKey(tc.header)
			if got != tc.wanted {
				t.Errorf("got %s, wanted %s", got, tc.wanted)
			}
		})
	}

}
