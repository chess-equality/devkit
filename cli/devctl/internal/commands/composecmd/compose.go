package composecmd

import (
	"fmt"
	"strings"

	"devkit/cli/devctl/internal/cmdregistry"
	runner "devkit/cli/devctl/internal/runner"
)

// Register adds compose lifecycle commands to the registry.
func Register(r *cmdregistry.Registry) {
	r.Register("up", handleUp)
	r.Register("down", handleDown)
	r.Register("restart", handleRestart)
	r.Register("status", handleStatus)
	r.Register("logs", handleLogs)
}

func ensureProject(ctx *cmdregistry.Context) error {
	if strings.TrimSpace(ctx.Project) == "" {
		return fmt.Errorf("-p <project> is required")
	}
	return nil
}

func handleUp(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	runner.Compose(ctx.DryRun, ctx.Files, "up", "-d")
	return nil
}

func handleDown(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	runner.Compose(ctx.DryRun, ctx.Files, "down")
	return nil
}

func handleRestart(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	runner.Compose(ctx.DryRun, ctx.Files, "restart")
	return nil
}

func handleStatus(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	runner.Compose(ctx.DryRun, ctx.Files, "ps")
	return nil
}

func handleLogs(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	args := append([]string{"logs"}, ctx.Args...)
	runner.Compose(ctx.DryRun, ctx.Files, args...)
	return nil
}
