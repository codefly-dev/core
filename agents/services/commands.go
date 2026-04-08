package services

import (
	"context"
	"fmt"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// CommandHandler is a function that executes a registered command.
type CommandHandler func(ctx context.Context, args []string) (string, error)

// Command pairs a definition with its handler.
type Command struct {
	Definition *agentv0.CommandDefinition
	Handler    CommandHandler
}

// CommandRegistry holds registered commands for an agent.
type CommandRegistry struct {
	commands map[string]*Command
}

// NewCommandRegistry creates an empty command registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{commands: make(map[string]*Command)}
}

// Register adds a command to the registry.
func (r *CommandRegistry) Register(def *agentv0.CommandDefinition, handler CommandHandler) {
	r.commands[def.Name] = &Command{Definition: def, Handler: handler}
	// Also register aliases
	for _, alias := range def.Aliases {
		r.commands[alias] = &Command{Definition: def, Handler: handler}
	}
}

// ListCommands returns all registered command definitions.
func (r *CommandRegistry) ListCommands() []*agentv0.CommandDefinition {
	// Deduplicate (aliases point to same command)
	seen := make(map[string]bool)
	var defs []*agentv0.CommandDefinition
	for _, cmd := range r.commands {
		if !seen[cmd.Definition.Name] {
			seen[cmd.Definition.Name] = true
			defs = append(defs, cmd.Definition)
		}
	}
	return defs
}

// Run executes a command by name.
func (r *CommandRegistry) Run(ctx context.Context, name string, args []string) (string, error) {
	cmd, ok := r.commands[name]
	if !ok {
		return "", fmt.Errorf("unknown command: %s", name)
	}
	return cmd.Handler(ctx, args)
}

// --- Base integration ---

// RegisterCommand registers a command on the agent's base.
func (s *Base) RegisterCommand(def *agentv0.CommandDefinition, handler CommandHandler) {
	if s.commands == nil {
		s.commands = NewCommandRegistry()
	}
	s.commands.Register(def, handler)
}

// ListCommands implements agentv0.AgentServer.
func (s *Base) ListCommands(_ context.Context, _ *agentv0.ListCommandsRequest) (*agentv0.ListCommandsResponse, error) {
	if s.commands == nil {
		return &agentv0.ListCommandsResponse{}, nil
	}
	return &agentv0.ListCommandsResponse{
		Commands: s.commands.ListCommands(),
	}, nil
}

// RunPluginCommand implements agentv0.AgentServer.
func (s *Base) RunPluginCommand(ctx context.Context, req *agentv0.RunPluginCommandRequest) (*agentv0.RunPluginCommandResponse, error) {
	if s.commands == nil {
		return &agentv0.RunPluginCommandResponse{
			Success: false,
			Error:   "no commands registered",
		}, nil
	}
	output, err := s.commands.Run(ctx, req.Command, req.Args)
	if err != nil {
		return &agentv0.RunPluginCommandResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return &agentv0.RunPluginCommandResponse{
		Success: true,
		Output:  output,
	}, nil
}
