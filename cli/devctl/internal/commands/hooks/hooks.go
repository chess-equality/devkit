package hooks

import (
	"fmt"
	"strings"

	"devkit/cli/devctl/internal/cmdregistry"
	"devkit/cli/devctl/internal/config"
	runner "devkit/cli/devctl/internal/runner"
)

// Register adds warm/maintain commands to the registry.
func Register(r *cmdregistry.Registry) {
	r.Register("warm", func(ctx *cmdregistry.Context) error { return handleHook(ctx, true) })
	r.Register("maintain", func(ctx *cmdregistry.Context) error { return handleHook(ctx, false) })
}

func handleHook(ctx *cmdregistry.Context, warm bool) error {
	project := strings.TrimSpace(ctx.Project)
	if project == "" {
		return fmt.Errorf("-p <project> is required")
	}
	hooks, _ := config.ReadHooks(ctx.Paths.Overlays, project)
	script := hooks.Maintain
	label := "maintain"
	if warm {
		script = hooks.Warm
		label = "warm"
	}
	if strings.TrimSpace(script) == "" {
		fmt.Printf("No %s hook defined\n", label)
		return nil
	}
	runner.Compose(ctx.DryRun, ctx.Files, "exec", "dev-agent", "bash", "-lc", script)
	return nil
}
