package services

import (
	"context"
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandRegistry_Register(t *testing.T) {
	r := NewCommandRegistry()

	r.Register(&agentv0.CommandDefinition{
		Name:        "test-cmd",
		Description: "A test command",
		Tags:        []string{"test"},
	}, func(ctx context.Context, args []string) (string, error) {
		return "hello", nil
	})

	cmds := r.ListCommands()
	require.Len(t, cmds, 1)
	assert.Equal(t, "test-cmd", cmds[0].Name)
	assert.Equal(t, "A test command", cmds[0].Description)
}

func TestCommandRegistry_Aliases(t *testing.T) {
	r := NewCommandRegistry()

	r.Register(&agentv0.CommandDefinition{
		Name:    "generate",
		Aliases: []string{"gen", "g"},
	}, func(ctx context.Context, args []string) (string, error) {
		return "generated", nil
	})

	// Should list as 1 command (not 3)
	cmds := r.ListCommands()
	require.Len(t, cmds, 1)
	assert.Equal(t, "generate", cmds[0].Name)

	// All names should resolve
	out, err := r.Run(context.Background(), "generate", nil)
	require.NoError(t, err)
	assert.Equal(t, "generated", out)

	out, err = r.Run(context.Background(), "gen", nil)
	require.NoError(t, err)
	assert.Equal(t, "generated", out)

	out, err = r.Run(context.Background(), "g", nil)
	require.NoError(t, err)
	assert.Equal(t, "generated", out)
}

func TestCommandRegistry_Run(t *testing.T) {
	r := NewCommandRegistry()

	r.Register(&agentv0.CommandDefinition{
		Name: "echo",
	}, func(ctx context.Context, args []string) (string, error) {
		if len(args) > 0 {
			return args[0], nil
		}
		return "empty", nil
	})

	out, err := r.Run(context.Background(), "echo", []string{"world"})
	require.NoError(t, err)
	assert.Equal(t, "world", out)

	out, err = r.Run(context.Background(), "echo", nil)
	require.NoError(t, err)
	assert.Equal(t, "empty", out)
}

func TestCommandRegistry_UnknownCommand(t *testing.T) {
	r := NewCommandRegistry()

	_, err := r.Run(context.Background(), "nope", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}

func TestBase_ListCommands_Empty(t *testing.T) {
	b := &Base{}
	resp, err := b.ListCommands(context.Background(), &agentv0.ListCommandsRequest{})
	require.NoError(t, err)
	// Base registers bash + bash_write automatically, but only after Load()
	// Without Load, commands is nil
	assert.Empty(t, resp.Commands)
}

func TestBase_RunPluginCommand_NoCommands(t *testing.T) {
	b := &Base{}
	resp, err := b.RunPluginCommand(context.Background(), &agentv0.RunPluginCommandRequest{
		Command: "test",
	})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Equal(t, "no commands registered", resp.Error)
}

func TestBase_RegisterAndRun(t *testing.T) {
	b := &Base{}
	b.RegisterCommand(&agentv0.CommandDefinition{
		Name: "greet",
	}, func(ctx context.Context, args []string) (string, error) {
		return "hello " + args[0], nil
	})

	resp, err := b.RunPluginCommand(context.Background(), &agentv0.RunPluginCommandRequest{
		Command: "greet",
		Args:    []string{"world"},
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "hello world", resp.Output)

	// List should include it
	listResp, err := b.ListCommands(context.Background(), &agentv0.ListCommandsRequest{})
	require.NoError(t, err)
	assert.Len(t, listResp.Commands, 1)
}
