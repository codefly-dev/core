package api

import "testing"

func TestServe(t *testing.T) {
	if Serve() == 0 {
		t.Fatal()
	}
}
