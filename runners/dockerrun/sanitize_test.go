package dockerrun

import "testing"

func TestSanitizeTerminalControl(t *testing.T) {
	esc := "\x1b"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text untouched", "hello world", "hello world"},
		{"SGR color kept", esc + "[31mred" + esc + "[0m", esc + "[31mred" + esc + "[0m"},
		{"cursor move stripped", "a" + esc + "[2Jb", "ab"},
		{"alt-screen toggle stripped", esc + "[?1049h" + "x", "x"},
		{"scroll region (DECSTBM) stripped", esc + "[1;40r" + "x", "x"},
		{"DSR cursor query stripped", esc + "[6n", ""},
		{"OSC query stripped (ST-terminated)", esc + "]11;?" + esc + "\\" + "x", "x"},
		{"OSC stripped (BEL-terminated)", esc + "]0;title" + "\x07" + "x", "x"},
		{"bare CR dropped", "a\rb", "ab"},
		{"BEL dropped", "a\x07b", "ab"},
		{"charset designation stripped", esc + "(B" + "x", "x"},
		{"color around stripped control kept", esc + "[32mok" + esc + "[6n" + "!" + esc + "[0m",
			esc + "[32mok" + "!" + esc + "[0m"},
		{"dangling ESC dropped", "tail" + esc, "tail"},
		{"tab preserved", "a\tb", "a\tb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(sanitizeTerminalControl([]byte(c.in)))
			if got != c.want {
				t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
