package wool_test

import (
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func TestFields(t *testing.T) {
	field := wool.Field("key", "string").Debug()
	log := wool.Log{
		Message: "message",
		Fields:  []*wool.LogField{field},
	}
	debug := log.AtLevel(wool.DEBUG)
	require.Equal(t, 1, len(debug.Fields))

	field = wool.Field("key", "string").Debug()
	log = wool.Log{
		Message: "message",
		Fields:  []*wool.LogField{field},
	}
	info := log.AtLevel(wool.INFO)
	require.Equal(t, 0, len(info.Fields))
}
