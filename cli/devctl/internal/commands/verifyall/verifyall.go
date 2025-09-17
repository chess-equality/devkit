package verifyall

import (
	"fmt"
	"strings"

	"devkit/cli/devctl/internal/cmdregistry"
	"devkit/cli/devctl/internal/runner"
)

// Register adds the verify-all command to the registry.
func Register(r *cmdregistry.Registry) {
	r.Register("verify-all", handle)
}

func handle(ctx *cmdregistry.Context) error {
	if strings.TrimSpace(ctx.Exe) == "" {
		return fmt.Errorf("executable path not provided")
	}
	runner.Host(ctx.DryRun, ctx.Exe, "-p", "codex", "verify")
	runner.Host(ctx.DryRun, ctx.Exe, "-p", "dev-all", "verify")
	return nil
}
