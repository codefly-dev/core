package shared_test

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
	"os/exec"
	"testing"
)

func TestWrapStart(t *testing.T) {
	logger := shared.NewLogger("shared.TestWrapStart")
	tests := []struct {
		name    string
		cmd     *exec.Cmd
		wantErr assert.ErrorAssertionFunc
	}{
		{"ls", exec.Command("ls", "testdata"), assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, shared.WrapStart(tt.cmd, logger), fmt.Sprintf("WrapStart(%v, %v)", tt.cmd, logger))
		})
	}
}
