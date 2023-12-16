package wool_test

import (
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/assert"
)

func TestFields(t *testing.T) {
	field := wool.Field("key", "string")
	log := wool.Log{
		Message: "message",
		Fields:  []*wool.LogField{field},
	}
	debug := log.AtLevel(wool.DEBUG)
	assert.Equal(t, 1, len(debug.Fields))

	field = wool.DebugField("key", "string")
	log = wool.Log{
		Message: "message",
		Fields:  []*wool.LogField{field},
	}
	info := log.AtLevel(wool.INFO)
	assert.Equal(t, 0, len(info.Fields))
}
