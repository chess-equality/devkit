package hosts

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"devkit/cli/devctl/internal/cmdregistry"
	"devkit/cli/devctl/internal/config"
	"devkit/cli/devctl/internal/execx"
	"devkit/cli/devctl/internal/hostsync"
)

const defaultHostsPath = "/etc/hosts"

type options struct {
	mode           string
	target         string
	index          int
	allAgents      bool
	requireIngress bool
	service        string
}

// Register adds the hosts command to the command registry.
func Register(r *cmdregistry.Registry) {
	r.Register("hosts", handle)
}

func handle(ctx *cmdregistry.Context) error {
	if strings.TrimSpace(ctx.Project) == "" {
		return fmt.Errorf("hosts requires -p <project>")
	}
	opts, err := parseOptions(ctx.Args)
	if err != nil {
		return err
	}
	cfg, _, err := config.ReadAll(ctx.Paths.OverlayPaths, ctx.Project)
	if err != nil {
		return err
	}
	if cfg.Ingress == nil || len(cfg.Ingress.Hosts) == 0 {
		if opts.requireIngress {
			return fmt.Errorf("overlay %s does not define ingress.hosts", ctx.Project)
		}
		fmt.Println("No ingress.hosts configured for overlay", ctx.Project)
		return nil
	}
	hosts := hostsync.CollectIngressHosts(cfg.Ingress.Hosts)
	if len(hosts) == 0 {
		if opts.requireIngress {
			return fmt.Errorf("overlay %s has empty ingress.hosts", ctx.Project)
		}
		fmt.Println("No ingress.hosts configured for overlay", ctx.Project)
		return nil
	}

	if opts.service == "" {
		opts.service = strings.TrimSpace(cfg.Service)
		if opts.service == "" {
			opts.service = "dev-agent"
		}
	}

	switch opts.mode {
	case "print":
		return runPrint(ctx, opts, hosts)
	case "apply":
		return runApply(ctx, opts, hosts)
	case "check":
		return runCheck(ctx, opts, hosts)
	default:
		return fmt.Errorf("unknown hosts mode %q", opts.mode)
	}
}

func parseOptions(args []string) (options, error) {
	out := options{
		mode:   "print",
		target: "all",
		index:  1,
	}
	if len(args) > 0 {
		first := strings.TrimSpace(args[0])
		if first != "" && !strings.HasPrefix(first, "--") {
			out.mode = first
			args = args[1:]
		}
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			if i+1 >= len(args) {
				return out, fmt.Errorf("--target requires value")
			}
			out.target = strings.TrimSpace(strings.ToLower(args[i+1]))
			i++
		case "--index":
			if i+1 >= len(args) {
				return out, fmt.Errorf("--index requires value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return out, fmt.Errorf("--index must be >= 1")
			}
			out.index = n
			i++
		case "--all-agents":
			out.allAgents = true
		case "--require-ingress":
			out.requireIngress = true
		case "--service":
			if i+1 >= len(args) {
				return out, fmt.Errorf("--service requires value")
			}
			out.service = strings.TrimSpace(args[i+1])
			i++
		default:
			return out, fmt.Errorf("unknown hosts flag %s", args[i])
		}
	}
	if out.mode != "print" && out.mode != "apply" && out.mode != "check" {
		return out, fmt.Errorf("usage: hosts [print|apply|check] [--target host|agents|all] [--index N] [--all-agents]")
	}
	if out.target != "host" && out.target != "agents" && out.target != "all" {
		return out, fmt.Errorf("--target must be host, agents, or all")
	}
	return out, nil
}

func runPrint(ctx *cmdregistry.Context, opts options, hosts []string) error {
	if opts.target == "host" || opts.target == "all" {
		fmt.Print(hostsync.RenderManagedBlock(ctx.Project, "127.0.0.1", hosts))
	}
	if opts.target == "agents" || opts.target == "all" {
		ingressIP, err := resolveIngressIP(ctx.Files)
		if err != nil {
			return err
		}
		fmt.Print(hostsync.RenderManagedBlock(ctx.Project+"-agents", ingressIP, hosts))
	}
	return nil
}

func runApply(ctx *cmdregistry.Context, opts options, hosts []string) error {
	if opts.target == "host" || opts.target == "all" {
		path := resolveHostsPath()
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated, err := hostsync.UpsertManagedBlock(string(existing), ctx.Project, "127.0.0.1", hosts)
		if err != nil {
			return err
		}
		if updated != string(existing) {
			if err := writeHostsFile(path, []byte(updated), ctx.DryRun); err != nil {
				return err
			}
			fmt.Printf("Applied host entries to %s\n", path)
		} else {
			fmt.Printf("Host entries already current in %s\n", path)
		}
	}
	if opts.target == "agents" || opts.target == "all" {
		ingressIP, err := resolveIngressIP(ctx.Files)
		if err != nil {
			return err
		}
		containers, err := resolveAgentContainers(ctx.Files, opts.service, opts.index, opts.allAgents)
		if err != nil {
			return err
		}
		for _, container := range containers {
			content, err := readContainerHosts(container)
			if err != nil {
				return err
			}
			updated, err := hostsync.UpsertManagedBlock(content, ctx.Project+"-agents", ingressIP, hosts)
			if err != nil {
				return err
			}
			if updated == content {
				fmt.Printf("Agent hosts already current in %s\n", container)
				continue
			}
			if err := writeContainerHosts(container, updated, ctx.DryRun); err != nil {
				return err
			}
			fmt.Printf("Applied agent entries to %s\n", container)
		}
	}
	return nil
}

func runCheck(ctx *cmdregistry.Context, opts options, hosts []string) error {
	failures := make([]string, 0)
	if opts.target == "host" || opts.target == "all" {
		path := resolveHostsPath()
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		missing := hostsync.MissingMappings(string(content), "127.0.0.1", hosts)
		if len(missing) > 0 {
			failures = append(failures, fmt.Sprintf("host %s missing mappings: %s", path, strings.Join(missing, ", ")))
		}
	}
	if opts.target == "agents" || opts.target == "all" {
		ingressIP, err := resolveIngressIP(ctx.Files)
		if err != nil {
			return err
		}
		containers, err := resolveAgentContainers(ctx.Files, opts.service, opts.index, opts.allAgents)
		if err != nil {
			return err
		}
		for _, container := range containers {
			content, err := readContainerHosts(container)
			if err != nil {
				return err
			}
			missing := hostsync.MissingMappings(content, ingressIP, hosts)
			if len(missing) > 0 {
				failures = append(failures, fmt.Sprintf("agent %s missing mappings: %s", container, strings.Join(missing, ", ")))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("hosts check failed:\n- %s", strings.Join(failures, "\n- "))
	}
	fmt.Println("Host mappings are current.")
	return nil
}

func resolveHostsPath() string {
	if path := strings.TrimSpace(os.Getenv("DEVKIT_HOSTS_FILE")); path != "" {
		return path
	}
	return defaultHostsPath
}

func writeHostsFile(path string, content []byte, dry bool) error {
	if dry {
		fmt.Printf("[dry-run] would write %s\n", path)
		return nil
	}
	if err := os.WriteFile(path, content, 0o644); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrPermission) {
		return err
	}

	ctx, cancel := execx.WithTimeout(30 * time.Second)
	defer cancel()
	res := execx.RunWithInput(ctx, content, "sudo", "tee", path)
	if res.Code != 0 {
		return fmt.Errorf("writing %s requires elevated permissions (sudo failed)", path)
	}
	return nil
}

func resolveIngressIP(files []string) (string, error) {
	name, err := firstContainerName(files, "ingress")
	if err != nil {
		return "", err
	}
	ctx, cancel := execx.WithTimeout(30 * time.Second)
	defer cancel()
	out, res := execx.Capture(ctx, "docker", "inspect", "-f", "{{range $name, $net := .NetworkSettings.Networks}}{{printf \"%s=%s\\n\" $name $net.IPAddress}}{{end}}", name)
	if res.Code != 0 {
		return "", fmt.Errorf("unable to inspect ingress container %s", name)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var fallback string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		networkName := strings.TrimSpace(parts[0])
		ip := strings.TrimSpace(parts[1])
		if ip == "" {
			continue
		}
		if strings.HasSuffix(networkName, "_dev-internal") {
			return ip, nil
		}
		if fallback == "" {
			fallback = ip
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("unable to resolve ingress IP from container %s", name)
}

func resolveAgentContainers(files []string, service string, index int, all bool) ([]string, error) {
	containers, err := listContainerNames(files, service)
	if err != nil {
		return nil, err
	}
	if len(containers) == 0 {
		return nil, fmt.Errorf("no running containers for service %s", service)
	}
	if all {
		return containers, nil
	}
	if index > len(containers) {
		return nil, fmt.Errorf("--index %d out of range (found %d containers)", index, len(containers))
	}
	return []string{containers[index-1]}, nil
}

func firstContainerName(files []string, service string) (string, error) {
	names, err := listContainerNames(files, service)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no running containers for service %s", service)
	}
	return names[0], nil
}

func listContainerNames(files []string, service string) ([]string, error) {
	ctx, cancel := execx.WithTimeout(30 * time.Second)
	defer cancel()
	args := append([]string{"compose"}, append(files, []string{"ps", "--format", "{{.Name}}", service}...)...)
	out, res := execx.Capture(ctx, "docker", args...)
	if res.Code != 0 {
		return nil, fmt.Errorf("failed to list containers for service %s", service)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			names = append(names, trimmed)
		}
	}
	sort.Strings(names)
	return names, nil
}

func readContainerHosts(container string) (string, error) {
	ctx, cancel := execx.WithTimeout(30 * time.Second)
	defer cancel()
	out, res := execx.Capture(ctx, "docker", "exec", container, "cat", defaultHostsPath)
	if res.Code != 0 {
		return "", fmt.Errorf("failed reading %s from %s", defaultHostsPath, container)
	}
	return out, nil
}

func writeContainerHosts(container string, content string, dry bool) error {
	if dry {
		fmt.Printf("[dry-run] would update %s in %s\n", defaultHostsPath, container)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := execx.RunWithInput(ctx, []byte(content), "docker", "exec", "-i", "-u", "0", container, "sh", "-lc", "cat > /etc/hosts")
	if res.Code != 0 {
		return fmt.Errorf("failed writing %s in %s", defaultHostsPath, container)
	}
	return nil
}
