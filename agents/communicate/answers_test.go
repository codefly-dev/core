package communicate_test

import (
	"testing"

	"github.com/codefly-dev/core/agents/communicate"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	"github.com/stretchr/testify/require"
)

const (
	HotReload    = "hot-reload"
	DatabaseName = "database-name"
)

func TestDefaults(t *testing.T) {
	questions := []*agentv0.Question{
		communicate.NewConfirm(&agentv0.Message{Name: HotReload, Message: "Migration hot-reload (Recommended)?", Description: "codefly can restart your database when migration changes detected 🔎"}, true),
		communicate.NewStringInput(&agentv0.Message{Name: DatabaseName, Message: "Name of the database?", Description: "Ensure encapsulation of your data"}, "database"),
	}
	hotReload, err := communicate.GetDefaultConfirm(questions, HotReload)
	require.NoError(t, err)
	require.True(t, hotReload)
	databaseName, err := communicate.GetDefaultStringInput(questions, DatabaseName)
	require.NoError(t, err)
	require.Equal(t, "database", databaseName)
}
