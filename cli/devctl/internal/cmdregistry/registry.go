package cmdregistry

import (
	"fmt"

	"devkit/cli/devctl/internal/compose"
	"devkit/cli/devctl/internal/config"
)

// Context carries the pre-parsed data and handles that command handlers need.
type Context struct {
	DryRun  bool
	Project string
	Profile string
	Args    []string
	Files   []string
	Paths   compose.Paths
	Pool    config.PoolConfig
	Exe     string
}

// Handler executes a command given the shared context.
type Handler func(*Context) error

// Registry maps command names to handlers.
type Registry struct {
	commands map[string]Handler
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{commands: make(map[string]Handler)}
}

// Register sets the handler for cmd. It panics if cmd already exists.
func (r *Registry) Register(cmd string, h Handler) {
	if _, exists := r.commands[cmd]; exists {
		panic(fmt.Sprintf("command %s already registered", cmd))
	}
	r.commands[cmd] = h
}

// Lookup returns the handler and whether it exists.
func (r *Registry) Lookup(cmd string) (Handler, bool) {
	h, ok := r.commands[cmd]
	return h, ok
}
