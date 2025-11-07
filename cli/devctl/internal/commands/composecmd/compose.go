package composecmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"devkit/cli/devctl/internal/cmdregistry"
	"devkit/cli/devctl/internal/execx"
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

// CleanupSharedInfra tears down stale containers/networks shared across overlays so the next `up`
// invocation (or layout apply) re-creates them with the correct compose project and IPAM settings.
func CleanupSharedInfra(dry bool, projectName string, fileArgs []string) {
	composeArgs := []string{"compose"}
	if strings.TrimSpace(projectName) != "" {
		composeArgs = append(composeArgs, "-p", projectName)
	}
	composeArgs = append(composeArgs, append([]string{}, fileArgs...)...)
	composeArgs = append(composeArgs, "down", "--remove-orphans")
	runner.HostBestEffort(dry, "docker", composeArgs...)

	// Shared container names across overlays (tinyproxy/dns/envoy) previously used fixed names.
	// Keep removing any lingering legacy containers unless tests opt out via DEVKIT_SKIP_SHARED_CLEANUP.
	if os.Getenv("DEVKIT_SKIP_SHARED_CLEANUP") != "1" {
		removeLegacyContainers(dry)
	}
	removeNetworksByProjectName(dry, projectName)
}

func containerExists(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	ctx, cancel := execx.WithTimeout(5 * time.Second)
	defer cancel()
	out, res := execx.Capture(ctx, "docker", "ps", "-aq", "--filter", fmt.Sprintf("name=^%s$", name))
	if res.Code != 0 {
		return false
	}
	return strings.TrimSpace(out) != ""
}

func networkExists(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	ctx, cancel := execx.WithTimeout(5 * time.Second)
	defer cancel()
	out, res := execx.Capture(ctx, "docker", "network", "ls", "--filter", fmt.Sprintf("name=^%s$", name), "--format", "{{.Name}}")
	if res.Code != 0 {
		return false
	}
	return strings.TrimSpace(out) != ""
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

func removeLegacyContainers(dry bool) {
	staleContainers := []string{"devkit_tinyproxy", "devkit_dns", "devkit_envoy", "devkit_envoy_sni"}
	filtered := make([]string, 0, len(staleContainers))
	for _, name := range staleContainers {
		if containerExists(name) {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return
	}
	rmArgs := append([]string{"rm", "-f"}, filtered...)
	runner.HostBestEffort(dry, "docker", rmArgs...)
}

func removeNetworksByProjectName(dry bool, projectName string) {
	proj := strings.TrimSpace(projectName)
	if proj == "" {
		proj = "devkit"
	}
	nets := []string{fmt.Sprintf("%s_dev-internal", proj), fmt.Sprintf("%s_dev-egress", proj)}
	for _, net := range nets {
		if networkExists(net) {
			runner.HostBestEffort(dry, "docker", "network", "rm", net)
		}
	}
}
