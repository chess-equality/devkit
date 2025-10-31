package verifyall

import (
	"fmt"
	"os"
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
	for _, proj := range []string{"codex", "dev-all"} {
		restore := withComposeProjectSelect(proj)
		runner.Host(ctx.DryRun, ctx.Exe, "-p", proj, "verify")
		if restore != nil {
			restore()
		}
	}
	return nil
}

func withComposeProjectSelect(project string) func() {
	name := resolveComposeProject(project)
	prev, had := os.LookupEnv("COMPOSE_PROJECT_NAME")
	if strings.TrimSpace(name) == "" {
		_ = os.Unsetenv("COMPOSE_PROJECT_NAME")
	} else {
		_ = os.Setenv("COMPOSE_PROJECT_NAME", name)
	}
	return func() {
		if had {
			_ = os.Setenv("COMPOSE_PROJECT_NAME", prev)
		} else {
			_ = os.Unsetenv("COMPOSE_PROJECT_NAME")
		}
	}
}

func resolveComposeProject(project string) string {
	if v := strings.TrimSpace(os.Getenv(composeProjectEnvKey(project))); v != "" {
		return v
	}
	return defaultProject(project)
}

func composeProjectEnvKey(project string) string {
	p := strings.TrimSpace(project)
	if p == "" {
		p = "DEFAULT"
	}
	upper := strings.ToUpper(p)
	var b strings.Builder
	for _, r := range upper {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return "DEVKIT_COMPOSE_PROJECT_" + b.String()
}

func defaultProject(project string) string {
	p := strings.TrimSpace(project)
	if p == "" || p == "codex" {
		return "devkit"
	}
	return "devkit-" + p
}
