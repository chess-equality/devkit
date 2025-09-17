package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"devkit/cli/devctl/internal/cmdregistry"
	allowcmd "devkit/cli/devctl/internal/commands/allow"
	composecmd "devkit/cli/devctl/internal/commands/composecmd"
	hookcmd "devkit/cli/devctl/internal/commands/hooks"
	networkcmd "devkit/cli/devctl/internal/commands/network"
	preflightcmd "devkit/cli/devctl/internal/commands/preflight"
	tmuxcmd "devkit/cli/devctl/internal/commands/tmuxcmd"
	verifyallcmd "devkit/cli/devctl/internal/commands/verifyall"
	"devkit/cli/devctl/internal/compose"
	"devkit/cli/devctl/internal/config"
	"devkit/cli/devctl/internal/execx"
	gitutil "devkit/cli/devctl/internal/gitutil"
	"devkit/cli/devctl/internal/layout"
	allow "devkit/cli/devctl/internal/netallow"
	"devkit/cli/devctl/internal/netutil"
	pth "devkit/cli/devctl/internal/paths"
	runner "devkit/cli/devctl/internal/runner"
	seed "devkit/cli/devctl/internal/seed"
	sshw "devkit/cli/devctl/internal/ssh"
	sshcfg "devkit/cli/devctl/internal/sshcfg"
	sshsteps "devkit/cli/devctl/internal/sshsteps"
	"devkit/cli/devctl/internal/tmuxutil"
	wtx "devkit/cli/devctl/internal/worktrees"
	// credential pool components
	assign "devkit/cli/devctl/internal/assign"
	poolcfg "devkit/cli/devctl/internal/config"
	pooldisc "devkit/cli/devctl/internal/pool"
)

func anchorHome(project string) string {
	if strings.TrimSpace(project) == "dev-all" {
		return "/workspaces/dev/.devhome"
	}
	return "/workspace/.devhome"
}

func anchorBase(project string) string {
	if strings.TrimSpace(project) == "dev-all" {
		return "/workspaces/dev/.devhomes"
	}
	return "/workspace/.devhomes"
}

// gitIdentityFromHost discovers a sensible git author/committer identity from the host.
// Priority:
// 1) DEVKIT_GIT_USER_NAME / DEVKIT_GIT_USER_EMAIL
// 2) GIT_AUTHOR_NAME / GIT_AUTHOR_EMAIL (falling back to COMMITTER_*)
// 3) `git config --global user.name` / `git config --global user.email`
func gitIdentityFromHost() (name, email string) {
	// Explicit override via env
	if v := strings.TrimSpace(os.Getenv("DEVKIT_GIT_USER_NAME")); v != "" {
		name = v
	}
	if v := strings.TrimSpace(os.Getenv("DEVKIT_GIT_USER_EMAIL")); v != "" {
		email = v
	}
	// Generic git envs
	if name == "" {
		if v := strings.TrimSpace(os.Getenv("GIT_AUTHOR_NAME")); v != "" {
			name = v
		}
		if name == "" {
			if v := strings.TrimSpace(os.Getenv("GIT_COMMITTER_NAME")); v != "" {
				name = v
			}
		}
	}
	if email == "" {
		if v := strings.TrimSpace(os.Getenv("GIT_AUTHOR_EMAIL")); v != "" {
			email = v
		}
		if email == "" {
			if v := strings.TrimSpace(os.Getenv("GIT_COMMITTER_EMAIL")); v != "" {
				email = v
			}
		}
	}
	// Host git config (best effort)
	if name == "" {
		if out, r := execx.Capture(context.Background(), "git", "config", "--global", "user.name"); r.Code == 0 {
			v := strings.TrimSpace(out)
			if v != "" {
				name = v
			}
		}
	}
	if email == "" {
		if out, r := execx.Capture(context.Background(), "git", "config", "--global", "user.email"); r.Code == 0 {
			v := strings.TrimSpace(out)
			if v != "" {
				email = v
			}
		}
	}
	return name, email
}

// shSingleQuote wraps s in single quotes and escapes any embedded single quotes for POSIX shells.
func shSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// listServiceNames returns running container names for a service (sorted) for the given compose files.
func listServiceNames(files []string, service string) []string {
	ctx, cancel := execx.WithTimeout(30 * time.Second)
	defer cancel()
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	args := append([]string{"compose"}, append(files, []string{"ps", "--format", "{{.Name}}", service}...)...)
	out, _ := execx.Capture(ctx, "docker", args...)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	names := make([]string, 0, len(lines))
	for _, s := range lines {
		s = strings.TrimSpace(s)
		if s != "" {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	return names
}

// listAgentNames returns running dev-agent container names (sorted) for the given compose files.
func listAgentNames(files []string) []string { return listServiceNames(files, "dev-agent") }

// listServiceNamesAny returns running containers for a service across all compose projects (fallback path).
func listServiceNamesAny(service string) []string {
	ctx, cancel := execx.WithTimeout(30 * time.Second)
	defer cancel()
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	out, _ := execx.Capture(ctx, "docker", "ps", "--filter", "label=com.docker.compose.service="+service, "--format", "{{.Names}}")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	names := make([]string, 0, len(lines))
	for _, s := range lines {
		s = strings.TrimSpace(s)
		if s != "" {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	return names
}

func pickByIndex(names []string, idx string) string {
	if len(names) == 0 {
		return ""
	}
	n := 1
	if i, err := strconv.Atoi(idx); err == nil && i >= 1 {
		n = i
	}
	if n > len(names) {
		n = len(names)
	}
	return names[n-1]
}

// execAgentIdx runs a bash script inside the Nth dev-agent container (1-based),
// avoiding compose --index brittleness by resolving container names via ps.
func execAgentIdx(dry bool, files []string, idx string, script string) {
	names := listAgentNames(files)
	if len(names) == 0 {
		names = listServiceNamesAny("dev-agent")
	}
	name := pickByIndex(names, idx)
	if strings.TrimSpace(name) == "" {
		if dry {
			// In dry-run, print a compose exec form for visibility and continue
			runner.Compose(dry, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", script)
			return
		}
		die("dev-agent not running")
	}
	runner.Host(dry, "docker", "exec", "-t", name, "bash", "-lc", script)
}

// execAgentIdxInput runs a bash script with stdin content inside the Nth dev-agent container.
func execAgentIdxInput(dry bool, files []string, idx string, input []byte, script string) {
	names := listAgentNames(files)
	if len(names) == 0 {
		names = listServiceNamesAny("dev-agent")
	}
	name := pickByIndex(names, idx)
	if strings.TrimSpace(name) == "" {
		if dry {
			// Dry-run: just echo a compose exec so the plan is visible
			runner.Compose(dry, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", script)
			return
		}
		die("dev-agent not running")
	}
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	_ = execx.RunWithInput(ctx, input, "docker", "exec", "-i", name, "bash", "-lc", script)
}

// execAgentIdxArgs runs an argv command inside the Nth dev-agent container without forcing a bash shell.
func execAgentIdxArgs(dry bool, files []string, idx string, argv ...string) {
	names := listAgentNames(files)
	if len(names) == 0 {
		names = listServiceNamesAny("dev-agent")
	}
	name := pickByIndex(names, idx)
	if strings.TrimSpace(name) == "" {
		if dry {
			// Show an equivalent compose exec for visibility
			runner.Compose(dry, files, append([]string{"exec", "-T", "--index", idx, "dev-agent"}, argv...)...)
			return
		}
		die("dev-agent not running")
	}
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	if dry {
		fmt.Fprintln(os.Stderr, "+ docker exec "+name+" "+strings.Join(argv, " "))
		return
	}
	all := append([]string{"exec", "-i", name}, argv...)
	res := execx.RunCtx(ctx, "docker", all...)
	if res.Code != 0 {
		os.Exit(res.Code)
	}
}

// execServiceIdx runs a bash script inside the Nth container for the given service name (1-based),
// falling back to docker ps by label if compose listing returns none.
func execServiceIdx(dry bool, files []string, service, idx string, script string) {
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	names := listServiceNames(files, service)
	if len(names) == 0 {
		names = listServiceNamesAny(service)
	}
	name := pickByIndex(names, idx)
	if strings.TrimSpace(name) == "" {
		if dry {
			runner.Compose(dry, files, "exec", "--index", idx, service, "bash", "-lc", script)
			return
		}
		die(service + " not running")
	}
	runner.Host(dry, "docker", "exec", "-t", name, "bash", "-lc", script)
}

// execServiceIdxInput runs a bash script with stdin content inside the Nth container for the given service.
func execServiceIdxInput(dry bool, files []string, service, idx string, input []byte, script string) {
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	names := listServiceNames(files, service)
	if len(names) == 0 {
		names = listServiceNamesAny(service)
	}
	name := pickByIndex(names, idx)
	if strings.TrimSpace(name) == "" {
		if dry {
			runner.Compose(dry, files, "exec", "--index", idx, service, "bash", "-lc", script)
			return
		}
		die(service + " not running")
	}
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	_ = execx.RunWithInput(ctx, input, "docker", "exec", "-i", name, "bash", "-lc", script)
}

// execServiceIdxArgs runs an argv command inside the Nth container for the given service without forcing a bash shell.
func execServiceIdxArgs(dry bool, files []string, service, idx string, argv ...string) {
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	names := listServiceNames(files, service)
	if len(names) == 0 {
		names = listServiceNamesAny(service)
	}
	name := pickByIndex(names, idx)
	if strings.TrimSpace(name) == "" {
		if dry {
			runner.Compose(dry, files, append([]string{"exec", "-T", "--index", idx, service}, argv...)...)
			return
		}
		die(service + " not running")
	}
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	if dry {
		fmt.Fprintln(os.Stderr, "+ docker exec "+name+" "+strings.Join(argv, " "))
		return
	}
	all := append([]string{"exec", "-i", name}, argv...)
	res := execx.RunCtx(ctx, "docker", all...)
	if res.Code != 0 {
		os.Exit(res.Code)
	}
}

// interactiveExecServiceIdx runs an interactive bash -lc inside the Nth container for service.
func interactiveExecServiceIdx(dry bool, files []string, service, idx string, bashCmd string) {
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	names := listServiceNames(files, service)
	if len(names) == 0 {
		names = listServiceNamesAny(service)
	}
	name := pickByIndex(names, idx)
	if strings.TrimSpace(name) == "" {
		if dry {
			runner.ComposeInteractive(dry, files, "exec", "--index", idx, service, "bash", "-lc", bashCmd)
			return
		}
		die(service + " not running")
	}
	runner.HostInteractive(dry, "docker", "exec", "-it", name, "bash", "-lc", bashCmd)
}

// resolveService returns the default service for a project overlay, falling back to dev-agent.
func resolveService(project string, root string) string {
	svc := "dev-agent"
	if strings.TrimSpace(project) == "" {
		return svc
	}
	if cfg, err := config.ReadAll(root, project); err == nil {
		if s := strings.TrimSpace(cfg.Service); s != "" {
			svc = s
		}
	}
	return svc
}

func usage() {
	fmt.Fprintf(os.Stderr, `devctl (Go) — experimental
Usage: devctl -p <project> [--profile <profiles>] <command> [args]

Commands:
  up, down, restart, status, logs
  scale N [--tmux-sync [--session NAME] [--name-prefix PFX] [--cd PATH] [--service NAME]],
  exec <n> <cmd...>, attach <n>
  allow <domain>, warm, maintain, check-net
  proxy {tinyproxy|envoy}
  tmux-shells [N], open [N], fresh-open [N]
  exec-cd <index> <subpath> [cmd...], attach-cd <index> <subpath>
  tmux-sync [--session NAME] [--count N] [--name-prefix PFX] [--cd PATH] [--service NAME]
  tmux-add-cd <index> <subpath> [--session NAME] [--name NAME] [--service NAME]
  tmux-apply-layout --file <layout.yaml> [--session NAME] [--attach]
  layout-apply --file <layout.yaml> [--attach]   (bring up overlays, then attach tmux)
  layout-generate [--service NAME] [--session NAME] [--output PATH]
  ssh-setup [--key path] [--index N], ssh-test [N]
  repo-config-ssh <repo> [--index N], repo-push-ssh <repo> [--index N]
  repo-config-https <repo> [--index N], repo-push-https <repo> [--index N]
  worktrees-init <repo> <count> [--base agent] [--branch main]
  worktrees-setup <repo> <count> [--base agent] [--branch main]  (dev-all)
  worktrees-branch <repo> <index> <branch>   (dev-all)
  worktrees-status <repo> [--all|--index N]  (dev-all)
  worktrees-sync <repo> (--pull|--push) [--all|--index N]  (dev-all)
  worktrees-tmux <repo> <count>              (dev-all)
  reset [N]                                  (alias: fresh-open)
  bootstrap <repo> <count>                   (dev-all)
  verify                                     (ssh + codex + worktrees)
  verify-all                                 (run verify for codex and dev-all)
  preflight                                  (host checks: docker, tmux, ssh keys, ~/.codex)

Flags:
  -p, --project   overlay project name (required for most)
  --profile       comma-separated: hardened,dns,envoy (default: dns)

Environment:
  DEVKIT_DEBUG=1  print executed commands
`)
}

func main() {
	var project string
	var profile string
	var dryRun bool
	var noTmux bool
	var noSeed bool
	var reSeed bool

	// rudimentary -p/--project and --profile parsing before subcmd
	args := os.Args[1:]
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-p", "--project":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "-p requires value")
				os.Exit(2)
			}
			project = args[i+1]
			i++
		case "--profile":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--profile requires value")
				os.Exit(2)
			}
			profile = args[i+1]
			i++
		case "--dry-run":
			dryRun = true
		case "--no-tmux":
			noTmux = true
		case "--no-seed":
			noSeed = true
		case "--reseed":
			reSeed = true
		case "-h", "--help", "help":
			usage()
			return
		default:
			out = append(out, a)
		}
	}
	args = out
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	exe, _ := os.Executable()
	paths, _ := compose.DetectPathsFromExe(exe)
	files, err := compose.Files(paths, project, profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// Preflight: choose a non-overlapping internal subnet and DNS IP if not explicitly set
	cidr, dns := netutil.PickInternalSubnet()
	// Export so docker compose can substitute in compose.dns.yml
	_ = os.Setenv("DEVKIT_INTERNAL_SUBNET", cidr)
	_ = os.Setenv("DEVKIT_DNS_IP", dns)
	if os.Getenv("DEVKIT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[devctl] internal subnet=%s dns_ip=%s\n", cidr, dns)
	}
	// Ensure codex overlay mounts the intended repo path via WORKSPACE_DIR
	if project == "codex" {
		devRoot := filepath.Clean(filepath.Join(paths.Root, ".."))
		_ = os.Setenv("WORKSPACE_DIR", filepath.Join(devRoot, "ouroboros-ide"))
	}

	// honor --no-tmux by setting env used by skipTmux()
	if noTmux {
		_ = os.Setenv("DEVKIT_NO_TMUX", "1")
	}

	cmd := args[0]
	sub := args[1:]
	// Read optional credential pool config from env (defaults preserve host behavior)
	pconf := poolcfg.ReadPoolConfig()
	registry := cmdregistry.New()
	allowcmd.Register(registry)
	composecmd.Register(registry)
	hookcmd.Register(registry)
	networkcmd.Register(registry)
	preflightcmd.Register(registry)
	verifyallcmd.Register(registry)
	tmuxcmd.Register(registry, doSyncTmux, ensureTmuxSessionWithWindow, defaultSessionName, mustAtoi, listServiceNames, buildWindowCmd, hasTmuxSession)
	ctx := &cmdregistry.Context{
		DryRun:  dryRun,
		Project: project,
		Profile: profile,
		Args:    sub,
		Files:   files,
		Paths:   paths,
		Pool:    pconf,
		Exe:     exe,
	}
	if handler, ok := registry.Lookup(cmd); ok {
		if err := handler(ctx); err != nil {
			die(err.Error())
		}
		return
	}
	switch cmd {
	case "scale":
		mustProject(project)
		n := "1"
		doTmuxSync := false
		sessName := ""
		namePrefix := "agent-"
		cdPath := ""
		// parse: scale N [--tmux-sync [--session NAME] [--name-prefix PFX] [--cd PATH] [--service NAME]]
		if len(sub) > 0 {
			n = sub[0]
		}
		service := "dev-agent"
		for i := 1; i < len(sub); i++ {
			switch sub[i] {
			case "--tmux-sync":
				doTmuxSync = true
			case "--session":
				if i+1 < len(sub) {
					sessName = sub[i+1]
					i++
				}
			case "--name-prefix":
				if i+1 < len(sub) {
					namePrefix = sub[i+1]
					i++
				}
			case "--cd":
				if i+1 < len(sub) {
					cdPath = sub[i+1]
					i++
				}
			case "--service":
				if i+1 < len(sub) {
					service = sub[i+1]
					i++
				}
			}
		}
		runner.Compose(dryRun, files, "up", "-d", "--scale", service+"="+n)
		if doTmuxSync && !skipTmux() {
			// Best effort: if tmux present, sync windows up to N
			doSyncTmux(dryRun, paths, project, files, sessName, namePrefix, cdPath, mustAtoi(n), service)
		}
	case "layout-apply":
		mustProject(project)
		layoutPath := ""
		doAttach := false
		for i := 0; i < len(sub); i++ {
			if sub[i] == "--file" && i+1 < len(sub) {
				layoutPath = sub[i+1]
				i++
			} else if sub[i] == "--attach" {
				doAttach = true
			}
		}
		if strings.TrimSpace(layoutPath) == "" {
			die("Usage: layout-apply --file <layout.yaml>")
		}
		lf, err := layout.Read(layoutPath)
		if err != nil {
			die(err.Error())
		}
		// 0) Optional: prepare host-side worktrees for dev-all overlays that request it
		for _, ov := range lf.Overlays {
			if strings.TrimSpace(ov.Project) != "dev-all" {
				continue
			}
			if ov.Worktrees == nil {
				continue
			}
			repo := strings.TrimSpace(ov.Worktrees.Repo)
			if repo == "" {
				continue
			}
			// Determine count/base/branch from worktrees block or fall back to overlay defaults
			count := ov.Worktrees.Count
			if count <= 0 {
				count = ov.Count
			}
			if count <= 0 {
				count = 1
			}
			baseBranch := strings.TrimSpace(ov.Worktrees.BaseBranch)
			branchPrefix := strings.TrimSpace(ov.Worktrees.BranchPrefix)
			if baseBranch == "" || branchPrefix == "" {
				if cfg, er := config.ReadAll(paths.Root, "dev-all"); er == nil {
					if baseBranch == "" {
						baseBranch = cfg.Defaults.BaseBranch
					}
					if branchPrefix == "" {
						branchPrefix = cfg.Defaults.BranchPrefix
					}
				}
				if baseBranch == "" {
					baseBranch = "main"
				}
				if branchPrefix == "" {
					branchPrefix = "agent"
				}
			}
			// Run worktree setup on host (idempotent). Use a generous timeout at the helper level.
			if err := wtx.Setup(paths.Root, repo, count, baseBranch, branchPrefix, dryRun); err != nil {
				die("worktrees setup failed: " + err.Error())
			}
		}
		// 0.5) Proactively tear down and remove networks for target compose projects to avoid CIDR/IP mismatch
		projSet := map[string]struct{}{}
		for _, ov := range lf.Overlays {
			pname := strings.TrimSpace(ov.ComposeProject)
			if pname == "" {
				pname = "devkit-" + strings.TrimSpace(ov.Project)
			}
			if pname == "" {
				continue
			}
			if _, seen := projSet[pname]; seen {
				continue
			}
			projSet[pname] = struct{}{}
		}
		for pname := range projSet {
			// Strict teardown to avoid stale networks with mismatched IPAM
			runner.Host(dryRun, "docker", "compose", "-p", pname, "down", "--remove-orphans")
			runner.HostBestEffort(dryRun, "docker", "network", "rm", pname+"_dev-internal", pname+"_dev-egress")
		}
		// 1) Bring up overlays with their own profiles and project names
		projMap := map[string]string{}
		for _, ov := range lf.Overlays {
			ovProj := strings.TrimSpace(ov.Project)
			if ovProj == "" {
				continue
			}
			filesOv, err := compose.Files(paths, ovProj, ov.Profiles)
			if err != nil {
				die(err.Error())
			}
			svc := ov.Service
			if strings.TrimSpace(svc) == "" {
				svc = "dev-agent"
			}
			cnt := ov.Count
			if cnt < 1 {
				cnt = 1
			}
			pname := ov.ComposeProject
			if strings.TrimSpace(pname) == "" {
				pname = "devkit-" + ovProj
			}
			projMap[ovProj] = pname
			args := []string{"up", "-d", "--scale", fmt.Sprintf("%s=%d", svc, cnt)}
			if ov.Build {
				args = append(args, "--build")
			}
			runner.ComposeWithProject(dryRun, pname, filesOv, args...)
		}
		// 1b) Ensure per-overlay SSH/Git is ready (anchor HOME + keys + global sshCommand)
		for _, ov := range lf.Overlays {
			ovProj := strings.TrimSpace(ov.Project)
			if ovProj == "" {
				continue
			}
			filesOv, err := compose.Files(paths, ovProj, ov.Profiles)
			if err != nil {
				die(err.Error())
			}
			svc := ov.Service
			if strings.TrimSpace(svc) == "" {
				svc = resolveService(ovProj, paths.Root)
			} else {
				svc = strings.TrimSpace(svc)
			}
			cnt := ov.Count
			if cnt < 1 {
				cnt = 1
			}
			// host keys and known_hosts
			hostEd := filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
			hostRsa := filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
			edBytes, _ := os.ReadFile(hostEd)
			rsaBytes, _ := os.ReadFile(hostRsa)
			known := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
			knownBytes, _ := os.ReadFile(known)
			for i := 1; i <= cnt; i++ {
				idx := fmt.Sprintf("%d", i)
				// ensure anchor and seed codex
				base := anchorBase(ovProj)
				anchor := anchorHome(ovProj)
				runAnchorPlan(dryRun, filesOv, svc, idx, seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true})
				// write ssh config + keys as available
				cfg := sshcfg.BuildGitHubConfigFor(anchor, len(edBytes) > 0, len(rsaBytes) > 0)
				if len(edBytes) > 0 {
					execServiceIdxInput(dryRun, filesOv, svc, idx, edBytes, "cat > '"+anchor+"'/.ssh/id_ed25519 && chmod 600 '"+anchor+"'/.ssh/id_ed25519")
					// write pub if present (best effort)
					pubBytes, _ := os.ReadFile(hostEd + ".pub")
					if len(pubBytes) > 0 {
						execServiceIdxInput(dryRun, filesOv, svc, idx, pubBytes, "cat > '"+anchor+"'/.ssh/id_ed25519.pub && chmod 644 '"+anchor+"'/.ssh/id_ed25519.pub")
					}
				}
				if len(rsaBytes) > 0 {
					execServiceIdxInput(dryRun, filesOv, svc, idx, rsaBytes, "cat > '"+anchor+"'/.ssh/id_rsa && chmod 600 '"+anchor+"'/.ssh/id_rsa")
				}
				if len(knownBytes) > 0 {
					execServiceIdxInput(dryRun, filesOv, svc, idx, knownBytes, "cat > '"+anchor+"'/.ssh/known_hosts && chmod 644 '"+anchor+"'/.ssh/known_hosts")
				}
				execServiceIdxInput(dryRun, filesOv, svc, idx, []byte(cfg), "cat > '"+anchor+"'/.ssh/config && chmod 600 '"+anchor+"'/.ssh/config")
				// configure global core.sshCommand in the anchor HOME
				execServiceIdx(dryRun, filesOv, svc, idx, "home='"+anchor+"'; HOME=\"$home\" git config --global core.sshCommand 'ssh -F ~/.ssh/config'")
			}
		}
		// 2) Apply windows into tmux using the composed project names
		sessName := strings.TrimSpace(lf.Session)
		if sessName == "" {
			sessName = defaultSessionName(project)
		}
		createdSession := false
		if !hasTmuxSession(sessName) {
			if len(lf.Windows) == 0 {
				die("no windows to create in session")
			}
			w := lf.Windows[0]
			idx := fmt.Sprintf("%d", w.Index)
			winProj := project
			if strings.TrimSpace(w.Project) != "" {
				winProj = w.Project
			}
			fargs, err := compose.Files(paths, winProj, profile)
			if err != nil {
				die(err.Error())
			}
			dest := layout.CleanPath(winProj, w.Path)
			svc := w.Service
			if strings.TrimSpace(svc) == "" {
				svc = "dev-agent"
			}
			name := w.Name
			if strings.TrimSpace(name) == "" {
				name = "agent-" + idx
			}
			pname := projMap[winProj]
			if strings.TrimSpace(pname) == "" {
				pname = "devkit-" + winProj
			}
			cmdStr := buildWindowCmdWithProject(fargs, winProj, idx, dest, svc, pname)
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sessName, cmdStr)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sessName+":0", name)...)
			createdSession = true
		}
		start := 0
		if createdSession {
			start = 1
		}
		for i := start; i < len(lf.Windows); i++ {
			w := lf.Windows[i]
			idx := fmt.Sprintf("%d", w.Index)
			winProj := project
			if strings.TrimSpace(w.Project) != "" {
				winProj = w.Project
			}
			fargs, err := compose.Files(paths, winProj, profile)
			if err != nil {
				die(err.Error())
			}
			dest := layout.CleanPath(winProj, w.Path)
			svc := w.Service
			if strings.TrimSpace(svc) == "" {
				svc = "dev-agent"
			}
			name := w.Name
			if strings.TrimSpace(name) == "" {
				name = "agent-" + idx
			}
			pname := projMap[winProj]
			if strings.TrimSpace(pname) == "" {
				pname = "devkit-" + winProj
			}
			cmdStr := buildWindowCmdWithProject(fargs, winProj, idx, dest, svc, pname)
			runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sessName, name, cmdStr)...)
		}
		if doAttach {
			runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sessName)...)
		}
	case "layout-generate":
		mustProject(project)
		// Inspect running containers to infer overlays (by compose project label) and counts per service.
		// Usage: layout-generate [--service NAME] [--session NAME] [--output PATH]
		service := "dev-agent"
		sessName := ""
		output := ""
		for i := 0; i < len(sub); i++ {
			switch sub[i] {
			case "--service":
				if i+1 < len(sub) {
					service = sub[i+1]
					i++
				}
			case "--session":
				if i+1 < len(sub) {
					sessName = sub[i+1]
					i++
				}
			case "--output":
				if i+1 < len(sub) {
					output = sub[i+1]
					i++
				}
			}
		}
		if strings.TrimSpace(sessName) == "" {
			sessName = defaultSessionName("mixed")
		}
		// docker ps labels: project/service
		type row struct{ Project, Service string }
		rows := []row{}
		{
			out, _ := execx.Capture(context.Background(), "docker", "ps", "--format", "{{.Label \"com.docker.compose.project\"}}\t{{.Label \"com.docker.compose.service\"}}")
			for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
				ln = strings.TrimSpace(ln)
				if ln == "" {
					continue
				}
				parts := strings.SplitN(ln, "\t", 2)
				if len(parts) != 2 {
					continue
				}
				rows = append(rows, row{Project: strings.TrimSpace(parts[0]), Service: strings.TrimSpace(parts[1])})
			}
		}
		// Count per project for the given service
		counts := map[string]int{}
		for _, r := range rows {
			if r.Service == service && r.Project != "" {
				counts[r.Project]++
			}
		}
		// Build YAML using detected compose project names; attempt to map overlay as suffix after "devkit-"
		var b strings.Builder
		fmt.Fprintf(&b, "session: %s\n\n", sessName)
		fmt.Fprintf(&b, "overlays:\n")
		type entry struct {
			ComposeProject, Overlay string
			Count                   int
		}
		entries := []entry{}
		for proj, c := range counts {
			// Guess overlay
			overlay := strings.TrimPrefix(proj, "devkit-")
			// ensure overlay folder exists
			if overlay == proj {
				// fallback: leave overlay as proj; windows will still use compose_project to exec
			}
			entries = append(entries, entry{ComposeProject: proj, Overlay: overlay, Count: c})
			fmt.Fprintf(&b, "  - project: %s\n    service: %s\n    count: %d\n    profiles: dns\n    compose_project: %s\n", overlay, service, c, proj)
		}
		fmt.Fprintf(&b, "\nwindows:\n")
		for _, e := range entries {
			for i := 1; i <= e.Count; i++ {
				name := e.Overlay
				if name == "" {
					name = e.ComposeProject
				}
				fmt.Fprintf(&b, "  - index: %d\n    project: %s\n    service: %s\n    path: /workspace\n    name: %s-%d\n", i, e.Overlay, service, name, i)
			}
		}
		yml := b.String()
		if strings.TrimSpace(output) == "" {
			fmt.Fprint(os.Stdout, yml)
		} else {
			if err := os.WriteFile(output, []byte(yml), 0644); err != nil {
				die(err.Error())
			}
			fmt.Fprintf(os.Stderr, "wrote %s\n", output)
		}
	case "exec":
		mustProject(project)
		if len(sub) == 0 {
			die("exec requires <index> and <cmd>")
		}
		idx := sub[0]
		rest := []string{}
		if len(sub) > 1 {
			rest = sub[1:]
		}
		// Enforce git identity
		gname, gemail := gitIdentityFromHost()
		if strings.TrimSpace(gname) == "" || strings.TrimSpace(gemail) == "" {
			die("git identity not configured. Set DEVKIT_GIT_USER_NAME and DEVKIT_GIT_USER_EMAIL, or configure host git --global user.name/user.email")
		}
		// Index-free HOME anchor per container (no reliance on replica index)
		// Proactively seed SSH+Git so 'git pull' just works in the window
		svc := resolveService(project, paths.Root)
		anchor := anchorHome(project)
		base := anchorBase(project)
		runAnchorPlan(dryRun, files, svc, idx, seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true})
		// copy keys + known_hosts + config under the anchor (best effort) and set global ssh
		{
			hostEd := filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
			hostRsa := filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
			edBytes, _ := os.ReadFile(hostEd)
			rsaBytes, _ := os.ReadFile(hostRsa)
			pubBytes, _ := os.ReadFile(hostEd + ".pub")
			known := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
			knownBytes, _ := os.ReadFile(known)
			cfg := sshcfg.BuildGitHubConfigFor(anchor, len(edBytes) > 0, len(rsaBytes) > 0)
			if len(edBytes) > 0 {
				execServiceIdxInput(dryRun, files, svc, idx, edBytes, "mkdir -p '"+anchor+"'/.ssh && cat > '"+anchor+"'/.ssh/id_ed25519 && chmod 600 '"+anchor+"'/.ssh/id_ed25519")
				if len(pubBytes) > 0 {
					execServiceIdxInput(dryRun, files, svc, idx, pubBytes, "mkdir -p '"+anchor+"'/.ssh && cat > '"+anchor+"'/.ssh/id_ed25519.pub && chmod 644 '"+anchor+"'/.ssh/id_ed25519.pub")
				}
			}
			if len(rsaBytes) > 0 {
				execServiceIdxInput(dryRun, files, svc, idx, rsaBytes, "mkdir -p '"+anchor+"'/.ssh && cat > '"+anchor+"'/.ssh/id_rsa && chmod 600 '"+anchor+"'/.ssh/id_rsa")
			}
			if len(knownBytes) > 0 {
				execServiceIdxInput(dryRun, files, svc, idx, knownBytes, "mkdir -p '"+anchor+"'/.ssh && cat > '"+anchor+"'/.ssh/known_hosts && chmod 644 '"+anchor+"'/.ssh/known_hosts")
			}
			execServiceIdxInput(dryRun, files, svc, idx, []byte(cfg), "mkdir -p '"+anchor+"'/.ssh && cat > '"+anchor+"'/.ssh/config && chmod 600 '"+anchor+"'/.ssh/config")
			execServiceIdx(dryRun, files, svc, idx, "home='"+anchor+"'; HOME=\"$home\" git config --global core.sshCommand 'ssh -F ~/.ssh/config'")
		}
		exports := "export HOME='" + anchor + "' CODEX_HOME='" + anchor + "/.codex' CODEX_ROLLOUT_DIR='" + anchor + "/.codex/rollouts' XDG_CACHE_HOME='" + anchor + "/.cache' XDG_CONFIG_HOME='" + anchor + "/.config' DEVKIT_GIT_USER_NAME='" + gname + "' DEVKIT_GIT_USER_EMAIL='" + gemail + "'"
		cmd := strings.Join(rest, " ")
		interactiveExecServiceIdx(dryRun, files, svc, idx, exports+"; exec "+cmd)
	case "attach":
		mustProject(project)
		idx := "1"
		if len(sub) > 0 {
			idx = sub[0]
		}
		// Long-lived attach: no timeout
		svc := resolveService(project, paths.Root)
		runner.ComposeInteractive(dryRun, files, "attach", "--index", idx, svc)
	case "proxy":
		mustProject(project)
		which := "tinyproxy"
		if len(sub) > 0 && strings.TrimSpace(sub[0]) != "" {
			which = sub[0]
		}
		switch which {
		case "tinyproxy":
			fmt.Println("Switching agent env to tinyproxy... (ensure overlay uses HTTP(S)_PROXY=http://tinyproxy:8888)")
		case "envoy":
			fmt.Println("Enable envoy profile: add --profile envoy to up/restart commands")
		default:
			die("unknown proxy: " + which)
		}
	case "codex-test":
		mustProject(project)
		// Parse optional args: [index] [repo]
		idx := "1"
		var repo string
		if len(sub) > 0 {
			if _, err := strconv.Atoi(sub[0]); err == nil {
				idx = sub[0]
				if len(sub) > 1 {
					repo = sub[1]
				}
			}
			if _, err := strconv.Atoi(sub[0]); err != nil {
				repo = sub[0]
				if len(sub) > 1 {
					idx = sub[1]
				}
			}
		}
		if project == "dev-all" && strings.TrimSpace(repo) == "" {
			if cfg, err := config.ReadAll(paths.Root, project); err == nil {
				repo = cfg.Defaults.Repo
			}
		}
		if strings.TrimSpace(repo) == "" {
			repo = "ouroboros-ide"
		}
		// Determine working directory/home inside container using helpers
		wd := pth.AgentRepoPath(project, idx, repo)
		home := pth.AgentHomePath(project, idx, repo)
		// Build a script that ensures HOME dirs and runs codex inside a repo dir
		script := fmt.Sprintf("set -euo pipefail; mkdir -p '%[1]s'/.codex/rollouts '%[1]s'/.cache '%[1]s'/.config '%[1]s'/.local; cd '%[2]s' 2>/dev/null || true; export HOME='%[1]s' CODEX_HOME='%[1]s'/.codex CODEX_ROLLOUT_DIR='%[1]s'/.codex/rollouts XDG_CACHE_HOME='%[1]s'/.cache XDG_CONFIG_HOME='%[1]s'/.config; if codex exec 'reply with: ok' 2>&1 | tr -d '\r' | grep -m1 -x ok >/dev/null; then echo ok; else echo 'codex-test failed'; exit 1; fi", home, wd)
		runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", script)
	case "verify":
		// Verify SSH to GitHub, Codex basic exec, and worktrees status (when applicable)
		mustProject(project)
		// 1) SSH test on agent 1
		{
			home := "/workspace/.devhome-agent1"
			script := fmt.Sprintf("set -e; home=%q; export HOME=\"$home\"; cfg=\"$home/.ssh/config\"; ssh -F \"$cfg\" -T github.com -o BatchMode=yes || true", home)
			execAgentIdx(dryRun, files, "1", script)
		}
		// 2) Codex basic check in-place
		{
			if project == "dev-all" {
				// Use defaults to pick a repo
				cfg, _ := config.ReadAll(paths.Root, project)
				repo := cfg.Defaults.Repo
				if strings.TrimSpace(repo) == "" {
					repo = "ouroboros-ide"
				}
				n := cfg.Defaults.Agents
				if n < 1 {
					n = 2
				}
				// ensure desired scale is up
				runner.Compose(dryRun, files, "up", "-d", "--scale", fmt.Sprintf("dev-agent=%d", n))
				base := "/workspaces/dev"
				wd := filepath.Join(base, repo)
				home := filepath.Join(base, repo, ".devhome-agent1")
				script := fmt.Sprintf("set -e; cd '%s' 2>/dev/null || true; HOME='%s' CODEX_HOME='%s/.codex' CODEX_ROLLOUT_DIR='%s/.codex/rollouts' XDG_CACHE_HOME='%s/.cache' XDG_CONFIG_HOME='%s/.config' codex exec 'reply with: ok' || true", wd, home, home, home, home, home)
				execAgentIdx(dryRun, files, "1", script)
				// quick worktrees status across up to 3 agents
				for i := 1; i <= n && i <= 3; i++ {
					is := fmt.Sprintf("%d", i)
					path := wd
					if i != 1 {
						path = filepath.Join(base, pth.AgentWorktreesDir, "agent"+is, repo)
					}
					execAgentIdx(dryRun, files, is, "cd '"+path+"' 2>/dev/null && git status -sb && git rev-parse --abbrev-ref --symbolic-full-name @{u} && git config --get push.default || true")
				}
			} else {
				// codex overlay: run from /workspace
				script := "set -e; cd /workspace 2>/dev/null || true; HOME=/workspace/.devhome-agent1 CODEX_HOME=/workspace/.devhome-agent1/.codex CODEX_ROLLOUT_DIR=/workspace/.devhome-agent1/.codex/rollouts XDG_CACHE_HOME=/workspace/.devhome-agent1/.cache XDG_CONFIG_HOME=/workspace/.devhome-agent1/.config codex exec 'reply with: ok' || true"
				execAgentIdx(dryRun, files, "1", script)
			}
		}
		fmt.Println("verify completed")
	case "codex-debug":
		mustProject(project)
		idx := "1"
		if len(sub) > 0 {
			idx = sub[0]
		}
		home := "/workspace/.devhome-agent" + idx
		script := fmt.Sprintf(`set -e
export HOME='%s'
export CODEX_HOME='%s/.codex'
export CODEX_ROLLOUT_DIR='%s/.codex/rollouts'
export XDG_CACHE_HOME='%s/.cache'
export XDG_CONFIG_HOME='%s/.config'
echo "HOME=$HOME"; echo "CODEX_HOME=$CODEX_HOME"
echo "-- locations --"
for p in "$HOME/.codex/auth.json" "$CODEX_HOME/auth.json" "/var/auth.json" "/var/host-codex/auth.json"; do
  [ -n "$p" ] || continue; echo -n "$p : "; [ -f "$p" ] && wc -c < "$p" || echo "(missing)"; done
exit 0`, home, home, home, home, home)
		runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", script)
	case "check-claude":
		mustProject(project)
		idx := "1"
		if len(sub) > 0 {
			idx = sub[0]
		}
		fmt.Println("== Env vars ==")
		runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", "env | grep -E '^HTTPS?_PROXY=|^NO_PROXY=' || true")
		fmt.Println("== Curl checks (through proxy) ==")
		runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", "set -e; echo -n 'claude.ai            : '; curl -sSvo /dev/null -w '%{http_code}\\n' https://claude.ai || true")
		runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", "set -e; echo -n 'anthropic.com        : '; curl -sSvo /dev/null -w '%{http_code}\\n' https://www.anthropic.com || true")
		home := fmt.Sprintf("/workspace/.devhome-agent%s", idx)
		runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", "mkdir -p '"+home+"'; HOME='"+home+"' timeout 15s claude --version || claude --help || true")
	case "check-sts":
		mustProject(project)
		which := "envoy"
		if len(sub) > 0 {
			which = strings.TrimSpace(sub[0])
		}
		var px string
		switch which {
		case "envoy":
			px = "http://envoy:3128"
		case "tinyproxy":
			px = "http://tinyproxy:8888"
		default:
			die("Usage: check-sts [envoy|tinyproxy]")
		}
		runner.Compose(dryRun, files, "exec", "dev-agent", "bash", "-lc", "HTTPS_PROXY='"+px+"' HTTP_PROXY='"+px+"' curl -sSvo /dev/null -w '%{http_code}\\n' https://sts.amazonaws.com || true")
		runner.Compose(dryRun, files, "exec", "dev-agent", "bash", "-lc", "HTTPS_PROXY='"+px+"' HTTP_PROXY='"+px+"' aws sts get-caller-identity || true")
		runner.Compose(dryRun, files, "exec", "dev-agent", "bash", "-lc", "curl -sSvo /dev/null -w '%{http_code}\\n' https://sts.amazonaws.com || true")
		runner.Compose(dryRun, files, "exec", "dev-agent", "bash", "-lc", "aws sts get-caller-identity || true")
	case "exec-cd":
		mustProject(project)
		if len(sub) < 2 {
			die("Usage: exec-cd <index> <subpath> [cmd...]")
		}
		idx := sub[0]
		subpath := sub[1]
		dest := subpath
		if !strings.HasPrefix(subpath, "/") {
			dest = filepath.Join("/workspaces/dev", subpath)
		}
		// Compute a sensible per-agent HOME for dev-all based on the destination path
		repo := "ouroboros-ide"
		if project == "dev-all" {
			rel := strings.TrimPrefix(dest, "/workspaces/dev/")
			parts := strings.Split(rel, "/")
			if len(parts) > 0 {
				if strings.HasPrefix(parts[0], "agent") && len(parts) > 1 {
					repo = parts[1]
				} else {
					repo = parts[0]
				}
			}
			if strings.TrimSpace(repo) == "" {
				repo = "ouroboros-ide"
			}
		}
		cmdstr := "bash"
		if len(sub) > 2 {
			cmdstr = strings.Join(sub[2:], " ")
		}
		// Interactive shell: ensure anchor and seed SSH, then cd
		svc := resolveService(project, paths.Root)
		anchor := anchorHome(project)
		base := anchorBase(project)
		runAnchorPlan(dryRun, files, svc, idx, seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true})
		{
			hostEd := filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
			hostRsa := filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
			edBytes, _ := os.ReadFile(hostEd)
			rsaBytes, _ := os.ReadFile(hostRsa)
			known := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
			knownBytes, _ := os.ReadFile(known)
			cfg := sshcfg.BuildGitHubConfigFor(anchor, len(edBytes) > 0, len(rsaBytes) > 0)
			if len(edBytes) > 0 {
				execServiceIdxInput(dryRun, files, svc, idx, edBytes, "cat > '"+anchor+"'/.ssh/id_ed25519 && chmod 600 '"+anchor+"'/.ssh/id_ed25519")
			}
			if len(rsaBytes) > 0 {
				execServiceIdxInput(dryRun, files, svc, idx, rsaBytes, "cat > '"+anchor+"'/.ssh/id_rsa && chmod 600 '"+anchor+"'/.ssh/id_rsa")
			}
			if len(knownBytes) > 0 {
				execServiceIdxInput(dryRun, files, svc, idx, knownBytes, "cat > '"+anchor+"'/.ssh/known_hosts && chmod 644 '"+anchor+"'/.ssh/known_hosts")
			}
			execServiceIdxInput(dryRun, files, svc, idx, []byte(cfg), "mkdir -p '"+anchor+"'/.ssh && cat > '"+anchor+"'/.ssh/config && chmod 600 '"+anchor+"'/.ssh/config")
			execServiceIdx(dryRun, files, svc, idx, "home='"+anchor+"'; HOME=\"$home\" git config --global core.sshCommand 'ssh -F ~/.ssh/config'")
		}
		exports := "export HOME='" + anchor + "' CODEX_HOME='" + anchor + "/.codex' CODEX_ROLLOUT_DIR='" + anchor + "/.codex/rollouts' XDG_CACHE_HOME='" + anchor + "/.cache' XDG_CONFIG_HOME='" + anchor + "/.config'"
		interactiveExecServiceIdx(dryRun, files, svc, idx, exports+"; cd '"+dest+"' && exec "+cmdstr)
	case "attach-cd":
		mustProject(project)
		if len(sub) < 2 {
			die("Usage: attach-cd <index> <subpath>")
		}
		idx := sub[0]
		subpath := sub[1]
		dest := subpath
		if !strings.HasPrefix(subpath, "/") {
			dest = filepath.Join("/workspaces/dev", subpath)
		}
		repo := "ouroboros-ide"
		if project == "dev-all" {
			rel := strings.TrimPrefix(dest, "/workspaces/dev/")
			parts := strings.Split(rel, "/")
			if len(parts) > 0 {
				if strings.HasPrefix(parts[0], "agent") && len(parts) > 1 {
					repo = parts[1]
				} else {
					repo = parts[0]
				}
			}
			if strings.TrimSpace(repo) == "" {
				repo = "ouroboros-ide"
			}
		}
		// Interactive shell: no timeout, index-free anchor
		svc := resolveService(project, paths.Root)
		anchor := "/workspace/.devhome"
		base := "/workspace/.devhomes"
		if project == "dev-all" {
			base = "/workspaces/dev/.devhomes"
		}
		cfg := seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true}
		runAnchorPlan(dryRun, files, svc, idx, cfg)
		exports := "export HOME='" + anchor + "' CODEX_HOME='" + anchor + "/.codex' CODEX_ROLLOUT_DIR='" + anchor + "/.codex/rollouts' XDG_CACHE_HOME='" + anchor + "/.cache' XDG_CONFIG_HOME='" + anchor + "/.config'"
		interactiveExecServiceIdx(dryRun, files, svc, idx, exports+"; cd '"+dest+"' && exec bash")
	case "tmux-shells":
		mustProject(project)
		n := 2
		if len(sub) > 0 {
			n = mustAtoi(sub[0])
		}
		runner.Compose(dryRun, files, "up", "-d", "--scale", fmt.Sprintf("dev-agent=%d", n))
		sess := "devkit-shells"
		// window 1
		home1 := "/workspace/.devhome-agent1"
		if !skipTmux() {
			names := listAgentNames(files)
			if len(names) == 0 {
				die("no dev-agent containers running")
			}
			cmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd /workspace; exec bash'", names[0], home1, home1, home1, home1, home1, home1, home1, home1, home1)
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n && i <= len(names); i++ {
				homei := fmt.Sprintf("/workspace/.devhome-agent%d", i)
				wcmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd /workspace; exec bash'", names[i-1], homei, homei, homei, homei, homei, homei, homei, homei, homei)
				runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sess, fmt.Sprintf("agent-%d", i), wcmd)...)
			}
			// tmux attach is long-lived: no timeout
			runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sess)...)
		}
	case "open":
		mustProject(project)
		n := 2
		if len(sub) > 0 {
			n = mustAtoi(sub[0])
		}
		runner.Compose(dryRun, files, "up", "-d", "--scale", fmt.Sprintf("dev-agent=%d", n))
		sess := "devkit-open"
		home1 := "/workspace/.devhome-agent1"
		if !skipTmux() {
			names := listAgentNames(files)
			if len(names) == 0 {
				die("no dev-agent containers running")
			}
			cmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd /workspace; exec bash'", names[0], home1, home1, home1, home1, home1, home1, home1, home1, home1)
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n && i <= len(names); i++ {
				homei := fmt.Sprintf("/workspace/.devhome-agent%d", i)
				wcmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd /workspace; exec bash'", names[i-1], homei, homei, homei, homei, homei, homei, homei, homei, homei)
				runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sess, fmt.Sprintf("agent-%d", i), wcmd)...)
			}
			runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sess)...)
		}
	case "fresh-open":
		mustProject(project)
		n := 3
		if len(sub) > 0 {
			n = mustAtoi(sub[0])
		}
		all := compose.AllProfilesFiles(paths, project)
		// If pool mode, include pool compose file to mount read-only pool
		if pconf.Mode == poolcfg.CredModePool {
			all = append(all, "-f", filepath.Join(paths.Kit, "compose.pool.yml"))
		}
		// bring everything down and cleanup
		runner.Compose(dryRun, all, "down")
		if !skipTmux() {
			runner.HostBestEffort(dryRun, "tmux", "kill-session", "-t", "devkit-open")
		}
		if !skipTmux() {
			runner.HostBestEffort(dryRun, "tmux", "kill-session", "-t", "devkit-shells")
		}
		if !skipTmux() {
			runner.HostBestEffort(dryRun, "tmux", "kill-session", "-t", "devkit-worktrees")
		}
		runner.HostBestEffort(dryRun, "docker", "rm", "-f", "devkit_envoy", "devkit_envoy_sni", "devkit_dns", "devkit_tinyproxy")
		runner.HostBestEffort(dryRun, "docker", "network", "rm", "devkit_dev-internal", "devkit_dev-egress")
		// start up with all profiles
		runner.Compose(dryRun, all, "up", "-d", "--scale", fmt.Sprintf("dev-agent=%d", n))
		// Seeding: pool mode (if configured and slots available) else fallback to host seeding
		if pconf.Mode == poolcfg.CredModePool {
			slots, _ := pooldisc.Discover("/var/codex-pool")
			if len(slots) > 0 {
				var asn assign.Assigner
				if pconf.Strategy == poolcfg.StrategyShuffle {
					asn = assign.NewShuffle(len(slots), pconf.Seed)
				} else {
					asn = assign.ByIndex{}
				}
				for j := 1; j <= n; j++ {
					homej := fmt.Sprintf("/workspace/.devhome-agent%d", j)
					slot := asn.Assign(slots, j, n)
					// Reset + copy from slot (resilient container targeting)
					for _, st := range seed.BuildResetPlan(homej).Steps {
						execAgentIdxArgs(dryRun, all, fmt.Sprintf("%d", j), st.Cmd...)
					}
					for _, st := range seed.BuildCopyFrom(slot.Path, homej).Steps {
						execAgentIdxArgs(dryRun, all, fmt.Sprintf("%d", j), st.Cmd...)
					}
					fmt.Printf("[seed] Agent %d -> slot %s\n", j, slot.Name)
				}
			} else {
				// No slots; fall back to host seeding
				for j := 1; j <= n; j++ {
					homej := fmt.Sprintf("/workspace/.devhome-agent%d", j)
					for _, script := range seed.BuildSeedScripts(homej) {
						execAgentIdx(dryRun, all, fmt.Sprintf("%d", j), script)
					}
				}
			}
		} else {
			// Host seeding (current behavior)
			for j := 1; j <= n; j++ {
				homej := fmt.Sprintf("/workspace/.devhome-agent%d", j)
				for _, script := range seed.BuildSeedScripts(homej) {
					execAgentIdx(dryRun, all, fmt.Sprintf("%d", j), script)
				}
			}
		}

		// tmux session
		if !skipTmux() {
			sess := "devkit-open"
			cmd := buildWindowCmd(all, project, "1", "/workspace", "dev-agent")
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n; i++ {
				wcmd := buildWindowCmd(all, project, fmt.Sprintf("%d", i), "/workspace", "dev-agent")
				runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sess, fmt.Sprintf("agent-%d", i), wcmd)...)
			}
			// tmux attach is long-lived: no timeout
			runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sess)...)
		}
	case "reset":
		// Alias to fresh-open with identical behavior
		mustProject(project)
		n := 3
		if len(sub) > 0 {
			n = mustAtoi(sub[0])
		}
		all := compose.AllProfilesFiles(paths, project)
		runner.Compose(dryRun, all, "down")
		if !skipTmux() {
			runner.HostBestEffort(dryRun, "tmux", "kill-session", "-t", "devkit-open")
		}
		if !skipTmux() {
			runner.HostBestEffort(dryRun, "tmux", "kill-session", "-t", "devkit-shells")
		}
		if !skipTmux() {
			runner.HostBestEffort(dryRun, "tmux", "kill-session", "-t", "devkit-worktrees")
		}
		runner.HostBestEffort(dryRun, "docker", "rm", "-f", "devkit_envoy", "devkit_envoy_sni", "devkit_dns", "devkit_tinyproxy")
		runner.HostBestEffort(dryRun, "docker", "network", "rm", "devkit_dev-internal", "devkit_dev-egress")
		// include pool compose file in reset as well
		if pconf.Mode == poolcfg.CredModePool {
			all = append(all, "-f", filepath.Join(paths.Kit, "compose.pool.yml"))
		}
		runner.Compose(dryRun, all, "up", "-d", "--scale", fmt.Sprintf("dev-agent=%d", n))
		if pconf.Mode == poolcfg.CredModePool {
			slots, _ := pooldisc.Discover("/var/codex-pool")
			if len(slots) > 0 {
				var asn assign.Assigner
				if pconf.Strategy == poolcfg.StrategyShuffle {
					asn = assign.NewShuffle(len(slots), pconf.Seed)
				} else {
					asn = assign.ByIndex{}
				}
				for j := 1; j <= n; j++ {
					homej := fmt.Sprintf("/workspace/.devhome-agent%d", j)
					slot := asn.Assign(slots, j, n)
					for _, st := range seed.BuildResetPlan(homej).Steps {
						execAgentIdxArgs(dryRun, all, fmt.Sprintf("%d", j), st.Cmd...)
					}
					for _, st := range seed.BuildCopyFrom(slot.Path, homej).Steps {
						execAgentIdxArgs(dryRun, all, fmt.Sprintf("%d", j), st.Cmd...)
					}
					fmt.Printf("[seed] Agent %d -> slot %s\n", j, slot.Name)
				}
			} else {
				for j := 1; j <= n; j++ {
					homej := fmt.Sprintf("/workspace/.devhome-agent%d", j)
					for _, script := range seed.BuildSeedScripts(homej) {
						execAgentIdx(dryRun, all, fmt.Sprintf("%d", j), script)
					}
				}
			}
		} else {
			for j := 1; j <= n; j++ {
				homej := fmt.Sprintf("/workspace/.devhome-agent%d", j)
				for _, script := range seed.BuildSeedScripts(homej) {
					execAgentIdx(dryRun, all, fmt.Sprintf("%d", j), script)
				}
			}
		}
		if !skipTmux() {
			sess := "devkit-open"
			cmd := buildWindowCmd(all, project, "1", "/workspace", "dev-agent")
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n; i++ {
				wcmd := buildWindowCmd(all, project, fmt.Sprintf("%d", i), "/workspace", "dev-agent")
				runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sess, fmt.Sprintf("agent-%d", i), wcmd)...)
			}
			runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sess)...)
		}
	case "ssh-setup":
		mustProject(project)
		// Parse flags: [--key path] [--index N]
		idx := "1"
		keyfile := ""
		for i := 0; i < len(sub); i++ {
			switch sub[i] {
			case "--key":
				if i+1 < len(sub) {
					keyfile = sub[i+1]
					i++
				}
			case "--index":
				if i+1 < len(sub) {
					idx = sub[i+1]
					i++
				}
			default:
				if keyfile == "" {
					keyfile = sub[i]
				}
			}
		}
		hostKey := keyfile
		if strings.TrimSpace(hostKey) == "" {
			hostKey = filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
		}
		if _, err := os.Stat(hostKey); err != nil {
			// fallback to rsa
			hostKey = filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
		}
		if _, err := os.Stat(hostKey); err != nil {
			die("Host key not found: " + hostKey)
		}
		pubPath := hostKey + ".pub"
		pubData, err := os.ReadFile(pubPath)
		if err != nil || len(pubData) == 0 {
			die("Public key not found: " + pubPath)
		}
		// allowlist + restart proxy/dns
		_, _, _ = allow.EnsureSSHGitHub(paths.Kit)
		runner.Compose(dryRun, files, "restart", "tinyproxy", "dns")
		// Compute per-agent HOME depending on overlay
		repoName := "ouroboros-ide"
		if project == "dev-all" {
			if cfg, err := config.ReadAll(paths.Root, project); err == nil && strings.TrimSpace(cfg.Defaults.Repo) != "" {
				repoName = cfg.Defaults.Repo
			}
		}
		// Index-free anchor home: /workspace/.devhome -> base/.devhomes/<container-id>
		anchor := "/workspace/.devhome"
		base := "/workspace/.devhomes"
		if project == "dev-all" {
			base = "/workspaces/dev/.devhomes"
		}
		{
			svc := resolveService(project, paths.Root)
			runAnchorPlan(dryRun, files, svc, idx, seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true})
		}
		// copy keys (attempt both types) and known_hosts
		keyBytes, _ := os.ReadFile(hostKey)
		// Also try to read the alternate key type
		hostEd := filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
		hostRsa := filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
		edBytes, _ := os.ReadFile(hostEd)
		rsaBytes, _ := os.ReadFile(hostRsa)
		known := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
		var knownBytes []byte
		if b, err := os.ReadFile(known); err == nil {
			knownBytes = b
		}
		// Determine which keys are present; prefer ed25519 and include rsa if available
		hasEd := len(edBytes) > 0
		hasRsa := len(rsaBytes) > 0
		if !hasEd && strings.HasSuffix(hostKey, "id_ed25519") && len(keyBytes) > 0 {
			hasEd = true
		}
		if !hasRsa && strings.HasSuffix(hostKey, "id_rsa") && len(keyBytes) > 0 {
			hasRsa = true
		}
		cfg := sshcfg.BuildGitHubConfigFor(anchor, hasEd, hasRsa)
		// Write whichever keys we have (ed25519 first, then rsa)
		// Use the originally selected key as well in case only one is available
		// ed25519
		if hasEd {
			if len(edBytes) == 0 {
				edBytes = keyBytes
			}
		}
		// rsa
		if hasRsa {
			if len(rsaBytes) == 0 {
				rsaBytes = keyBytes
			}
		}
		// Build input writes (private/public for ed25519 only; rsa can be private-only for ssh use)
		// We’ll call BuildWriteSteps once for ed25519 (if present) to place id_ed25519(.pub),
		// then manually write id_rsa if present.
		for _, step := range sshw.BuildWriteSteps(anchor, edBytes, pubData, knownBytes, cfg) {
			svc := resolveService(project, paths.Root)
			execServiceIdxInput(dryRun, files, svc, idx, step.Content, step.Script)
		}
		if hasRsa {
			svc := resolveService(project, paths.Root)
			// write id_rsa with 600 perms
			execServiceIdxInput(dryRun, files, svc, idx, rsaBytes, "cat > '"+anchor+"'/.ssh/id_rsa && chmod 600 '"+anchor+"'/.ssh/id_rsa")
		}
		// git config global sshCommand and repo-level sshCommand, then validate with a pull
		{
			repoPath := pth.AgentRepoPath(project, idx, repoName)
			for _, sc := range sshw.BuildConfigureScripts(anchor, repoPath) {
				svc := resolveService(project, paths.Root)
				execServiceIdx(dryRun, files, svc, idx, sc)
			}
		}
	case "ssh-test":
		mustProject(project)
		idx := "1"
		if len(sub) > 0 {
			idx = sub[0]
		}
		anchor := "/workspace/.devhome"
		{
			script := fmt.Sprintf("set -e; export HOME=%q; cfg=\"$HOME/.ssh/config\"; ssh -F \"$cfg\" -T github.com -o BatchMode=yes || true", anchor)
			svc := resolveService(project, paths.Root)
			execServiceIdx(dryRun, files, svc, idx, script)
		}
	case "repo-config-ssh":
		mustProject(project)
		if len(sub) < 1 {
			die("Usage: repo-config-ssh <repo-path> [--index N]")
		}
		repo := sub[0]
		idx := "1"
		if len(sub) >= 3 && sub[1] == "--index" {
			idx = sub[2]
		}
		base := "/workspace"
		if project == "dev-all" {
			base = "/workspaces/dev"
		}
		dest := base + "/" + repo
		if repo == "." || repo == "" {
			dest = base
		}
		home := "/workspace/.devhome-agent" + idx
		cmd := "set -euo pipefail; export HOME='" + home + "'; cd '" + dest + "'; url=$(git remote get-url origin 2>/dev/null || true); if [ -z \"$url\" ]; then echo 'No origin remote configured' >&2; exit 1; fi; if [[ \"$url\" =~ ^https://github.com/([^/]+)/([^/.]+)(\\.git)?$ ]]; then newurl=git@github.com:${BASH_REMATCH[1]}/${BASH_REMATCH[2]}.git; echo Setting SSH origin to \"$newurl\"; git remote set-url origin \"$newurl\"; else echo \"Origin already SSH: $url\"; fi"
		{
			svc := resolveService(project, paths.Root)
			execServiceIdx(dryRun, files, svc, idx, cmd)
		}
	case "repo-config-https":
		mustProject(project)
		if len(sub) < 1 {
			die("Usage: repo-config-https <repo-path> [--index N]")
		}
		repo := sub[0]
		idx := "1"
		if len(sub) >= 3 && sub[1] == "--index" {
			idx = sub[2]
		}
		base := "/workspace"
		if project == "dev-all" {
			base = "/workspaces/dev"
		}
		dest := base + "/" + repo
		if repo == "." || repo == "" {
			dest = base
		}
		cmd := "set -euo pipefail; cd '" + dest + "'; url=$(git remote get-url origin 2>/dev/null || true); if [ -z \"$url\" ]; then echo 'No origin remote configured' >&2; exit 1; fi; if [[ \"$url\" =~ ^git@github.com:([^/]+)/([^/.]+)(\\.git)?$ ]]; then newurl=https://github.com/${BASH_REMATCH[1]}/${BASH_REMATCH[2]}.git; echo Setting HTTPS origin to \"$newurl\"; git remote set-url origin \"$newurl\"; else echo \"Origin already HTTPS: $url\"; fi"
		{
			svc := resolveService(project, paths.Root)
			execServiceIdx(dryRun, files, svc, idx, cmd)
		}
	case "repo-push-ssh":
		mustProject(project)
		if len(sub) < 1 {
			die("Usage: repo-push-ssh <repo-path> [--index N]")
		}
		repo := sub[0]
		idx := "1"
		for i := 1; i+1 < len(sub); i++ {
			if sub[i] == "--index" {
				idx = sub[i+1]
			}
		}
		// best-effort ensure ssh
		// assemble dest and push
		base := "/workspace"
		if project == "dev-all" {
			base = "/workspaces/dev"
		}
		dest := base + "/" + repo
		if repo == "." || repo == "" {
			dest = base
		}
		home := "/workspace/.devhome-agent" + idx
		cmd := "set -euo pipefail; home=\"" + home + "\"; export HOME=\"$home\"; cd '" + dest + "'; cur=$(git rev-parse --abbrev-ref HEAD); url=$(git remote get-url origin 2>/dev/null || true); if [ -z \"$url\" ]; then echo 'No origin remote configured' >&2; exit 1; fi; if [[ \"$url\" =~ ^https://github.com/([^/]+)/([^/.]+)(\\.git)?$ ]]; then newurl=git@github.com:${BASH_REMATCH[1]}/${BASH_REMATCH[2]}.git; echo Setting SSH origin to \"$newurl\"; git remote set-url origin \"$newurl\"; fi; echo Pushing branch \"$cur\" to origin...; GIT_SSH_COMMAND=\"ssh -F \\\"$home/.ssh/config\\\"\" git push -u origin HEAD"
		{
			svc := resolveService(project, paths.Root)
			execServiceIdx(dryRun, files, svc, idx, cmd)
		}
	case "repo-push-https":
		mustProject(project)
		if len(sub) < 1 {
			die("Usage: repo-push-https <repo-path> [--index N]")
		}
		repo := sub[0]
		idx := "1"
		if len(sub) >= 3 && sub[1] == "--index" {
			idx = sub[2]
		}
		// ensure HTTPS config then push
		base := "/workspace"
		if project == "dev-all" {
			base = "/workspaces/dev"
		}
		dest := base + "/" + repo
		if repo == "." || repo == "" {
			dest = base
		}
		cmd := "set -euo pipefail; cd '" + dest + "'; echo Pushing branch $(git rev-parse --abbrev-ref HEAD) to origin...; git push -u origin HEAD"
		// call repo-config-https first? skipped for simplicity
		{
			svc := resolveService(project, paths.Root)
			runner.Compose(dryRun, files, "exec", "--index", idx, svc, "bash", "-lc", cmd)
		}
	case "worktrees-init":
		mustProject(project)
		if len(sub) < 2 {
			die("Usage: worktrees-init <repo> <count> [--base agent] [--branch main]")
		}
		repo := sub[0]
		count := sub[1]
		base := "agent"
		branch := "main"
		for i := 2; i+1 < len(sub); i++ {
			if sub[i] == "--base" {
				base = sub[i+1]
			} else if sub[i] == "--branch" {
				branch = sub[i+1]
			}
		}
		// create worktrees on host filesystem
		// primary at /workspaces/dev/<repo>, others at /workspaces/dev/agentN/<repo>
		// Here we just print guidance; actual creation may be outside scope.
		fmt.Printf("Initialize worktrees for %s: base=%s branch=%s (1..%s) on host (manual)\n", repo, base, branch, count)
	case "worktrees-setup":
		// Create per-agent branches and worktrees rooted in the dev root (dev-all overlay pattern)
		mustProject(project)
		if project != "dev-all" {
			die("Use -p dev-all for worktrees-setup")
		}
		if len(sub) < 2 {
			die("Usage: worktrees-setup <repo> <count> [--base agent] [--branch main]")
		}
		repo := sub[0]
		n := mustAtoi(sub[1])
		branchPrefix := "agent"
		baseBranch := "main"
		for i := 2; i+1 < len(sub); i++ {
			if sub[i] == "--base" {
				branchPrefix = sub[i+1]
			} else if sub[i] == "--branch" {
				baseBranch = sub[i+1]
			}
		}
		if err := wtx.Setup(paths.Root, repo, n, baseBranch, branchPrefix, dryRun); err != nil {
			die(err.Error())
		}
	case "run":
		// Idempotent end-to-end launcher: ensures worktrees, scales up, and opens tmux across N agents
		mustProject(project)
		if project != "dev-all" {
			die("Use -p dev-all for run")
		}
		if len(sub) < 2 {
			die("Usage: run <repo> <count>")
		}
		repo := sub[0]
		n := mustAtoi(sub[1])
		// Ensure any stale networks/containers are removed so the picked subnet/IP apply cleanly
		// (prevents mismatch when an existing devkit_dev-internal has different IPAM than our choice)
		// 1) Best-effort compose down with all profiles for this project to stop attached containers
		all := compose.AllProfilesFiles(paths, project)
		runner.Compose(dryRun, all, "down")
		// 2) Remove known sidecars and any lingering dev-agents
		runner.HostBestEffort(dryRun, "docker", "rm", "-f", "devkit_envoy", "devkit_envoy_sni", "devkit_dns", "devkit_tinyproxy")
		// Remove any devkit-dev-agent-* containers that may still be around and attached
		runner.HostBestEffort(dryRun, "bash", "-lc", "docker ps -aq --filter name='^devkit-dev-agent-' | xargs -r docker rm -f")
		// 3) Now remove networks (will succeed once no active endpoints remain)
		runner.HostBestEffort(dryRun, "docker", "network", "rm", "devkit_dev-internal", "devkit_dev-egress")
		// Ensure worktrees are present and configured (idempotent)
		if err := wtx.Setup(paths.Root, repo, n, "main", "agent", dryRun); err != nil {
			die(err.Error())
		}
		// Bring up and open tmux windows for N agents
		// Compose up with scale (remove orphans for idempotency)
		runner.Compose(dryRun, files, "up", "-d", "--remove-orphans", "--scale", fmt.Sprintf("dev-agent=%d", n))
		// Seed per-agent Codex HOME from host mounts so codex can run non-interactively
		if !noSeed || reSeed {
			// agent 1 per-agent home (outside repo path for safety)
			home1 := pth.AgentHomePath(project, "1", repo)
			for _, script := range seed.BuildSeedScripts(home1) {
				execAgentIdx(dryRun, files, "1", script)
			}
			// agents 2..n: home under agentN/<repo>
			for j := 2; j <= n; j++ {
				idx := fmt.Sprintf("%d", j)
				homej := pth.AgentHomePath(project, idx, repo)
				for _, script := range seed.BuildSeedScripts(homej) {
					execAgentIdx(dryRun, files, idx, script)
				}
			}
		}
		// Ensure sensitive local dirs are ignored by git inside each repo (defense-in-depth)
		{
			// agent1 repo path
			rp1 := pth.AgentRepoPath(project, "1", repo)
			execAgentIdx(dryRun, files, "1", gitutil.UpdateExcludeScript(rp1, ".devhome-agent*"))
			// other agents
			for j := 2; j <= n; j++ {
				idx := fmt.Sprintf("%d", j)
				rpj := pth.AgentRepoPath(project, idx, repo)
				execAgentIdx(dryRun, files, idx, gitutil.UpdateExcludeScript(rpj, ".devhome-agent*"))
			}
		}
		// Ensure SSH config per agent with correct HOME under repo paths, then validate git pull
		{
			// Make sure ssh.github.com is allowlisted and proxies are active before any git/ssh calls
			_, _, _ = allow.EnsureSSHGitHub(paths.Kit)
			runner.Compose(dryRun, files, "restart", "tinyproxy", "dns")
			hostKey := filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
			if _, err := os.Stat(hostKey); err != nil {
				hostKey = filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
			}
			keyBytes, _ := os.ReadFile(hostKey)
			pubBytes, _ := os.ReadFile(hostKey + ".pub")
			known := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
			knownBytes, _ := os.ReadFile(known)
			// agent 1
			home1 := pth.AgentHomePath(project, "1", repo)
			cfg1 := sshcfg.BuildGitHubConfig(home1)
			execAgentIdx(dryRun, files, "1", sshsteps.MkdirSSH(home1))
			for _, step := range sshw.BuildWriteSteps(home1, keyBytes, pubBytes, knownBytes, cfg1) {
				execAgentIdxInput(dryRun, files, "1", step.Content, step.Script)
			}
			for _, sc := range sshw.BuildConfigureScripts(home1, pth.AgentRepoPath(project, "1", repo)) {
				execAgentIdx(dryRun, files, "1", sc)
			}
			// agents 2..n
			for i := 2; i <= n; i++ {
				idx := fmt.Sprintf("%d", i)
				whome := pth.AgentHomePath(project, idx, repo)
				wpath := pth.AgentRepoPath(project, idx, repo)
				cfg := sshcfg.BuildGitHubConfig(whome)
				execAgentIdx(dryRun, files, idx, sshsteps.MkdirSSH(whome))
				for _, step := range sshw.BuildWriteSteps(whome, keyBytes, pubBytes, knownBytes, cfg) {
					execAgentIdxInput(dryRun, files, idx, step.Content, step.Script)
				}
				for _, sc := range sshw.BuildConfigureScripts(whome, wpath) {
					execAgentIdx(dryRun, files, idx, sc)
				}
			}
		}
		// Reuse tmux workflow
		sess := "devkit-worktrees"
		home1 := pth.AgentHomePath(project, "1", repo)
		if !skipTmux() {
			// Idempotency: kill existing session if present
			runner.HostBestEffort(dryRun, "tmux", "kill-session", "-t", sess)
			names := listAgentNames(files)
			if len(names) == 0 {
				die("no dev-agent containers running")
			}
			cmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd \"%s\"; exec bash'", names[0], home1, home1, home1, home1, home1, home1, home1, home1, home1, pth.AgentRepoPath(project, "1", repo))
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n; i++ {
				whome := pth.AgentHomePath(project, fmt.Sprintf("%d", i), repo)
				wpath := pth.AgentRepoPath(project, fmt.Sprintf("%d", i), repo)
				if i > len(names) {
					break
				}
				wcmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd \"%s\"; exec bash'", names[i-1], whome, whome, whome, whome, whome, whome, whome, whome, whome, wpath)
				runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sess, fmt.Sprintf("agent-%d", i), wcmd)...)
			}
			runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sess)...)
		}
	case "worktrees-branch":
		mustProject(project)
		if project != "dev-all" {
			die("Use -p dev-all for worktrees-branch")
		}
		if len(sub) < 3 {
			die("Usage: -p dev-all worktrees-branch <repo> <index> <branch>")
		}
		repo := sub[0]
		idx := sub[1]
		branch := sub[2]
		base := "/workspaces/dev"
		var path string
		if idx == "1" {
			path = base + "/" + repo
		} else {
			path = base + "/agent" + idx + "/" + repo
		}
		runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", "set -e; cd '"+path+"'; git checkout -b '"+branch+"'")
	case "worktrees-status":
		mustProject(project)
		if project != "dev-all" {
			die("Use -p dev-all for worktrees-status")
		}
		if len(sub) < 1 {
			die("Usage: -p dev-all worktrees-status <repo> [--all|--index N]")
		}
		repo := sub[0]
		idx := ""
		if len(sub) >= 3 && sub[1] == "--index" {
			idx = sub[2]
		}
		base := "/workspaces/dev"
		if idx != "" {
			path := base + "/" + repo
			if idx != "1" {
				path = base + "/agent" + idx + "/" + repo
			}
			runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", "set -e; cd '"+path+"'; git status -sb")
		} else {
			// sample for first two agents
			for _, i := range []string{"1", "2"} {
				path := base + "/" + repo
				if i != "1" {
					path = base + "/agent" + i + "/" + repo
				}
				runner.Compose(dryRun, files, "exec", "--index", i, "dev-agent", "bash", "-lc", "cd '"+path+"' 2>/dev/null && git status -sb || true")
			}
		}
	case "worktrees-sync":
		mustProject(project)
		if project != "dev-all" {
			die("Use -p dev-all for worktrees-sync")
		}
		if len(sub) < 2 {
			die("Usage: worktrees-sync <repo> (--pull|--push) [--all|--index N]")
		}
		repo := sub[0]
		op := sub[1]
		idx := ""
		if len(sub) >= 4 && sub[2] == "--index" {
			idx = sub[3]
		}
		base := "/workspaces/dev"
		gitcmd := "git pull --ff-only"
		if op == "--push" {
			gitcmd = "git push origin HEAD:main"
		}
		if idx != "" {
			path := base + "/" + repo
			if idx != "1" {
				path = base + "/agent" + idx + "/" + repo
			}
			runner.Compose(dryRun, files, "exec", "--index", idx, "dev-agent", "bash", "-lc", "set -e; cd '"+path+"'; "+gitcmd)
		} else {
			for _, i := range []string{"1", "2", "3", "4", "5", "6"} {
				path := base + "/" + repo
				if i != "1" {
					path = base + "/agent" + i + "/" + repo
				}
				runner.Compose(dryRun, files, "exec", "--index", i, "dev-agent", "bash", "-lc", "cd '"+path+"' 2>/dev/null && (set -e; cd '"+path+"'; "+gitcmd+") || true")
			}
		}
	case "worktrees-tmux":
		mustProject(project)
		if project != "dev-all" {
			die("Use -p dev-all for worktrees-tmux")
		}
		if len(sub) < 2 {
			die("Usage: -p dev-all worktrees-tmux <repo> <count>")
		}
		repo := sub[0]
		n := mustAtoi(sub[1])
		// Bring up and open tmux windows for N agents
		runner.Compose(dryRun, files, "up", "-d", "--scale", fmt.Sprintf("dev-agent=%d", n))
		sess := "devkit-worktrees"
		home1 := pth.AgentHomePath(project, "1", repo)
		if !skipTmux() {
			names := listAgentNames(files)
			if len(names) == 0 {
				die("no dev-agent containers running")
			}
			cmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd \"%s\"; exec bash'", names[0], home1, home1, home1, home1, home1, home1, home1, home1, home1, pth.AgentRepoPath(project, "1", repo))
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n; i++ {
				whome := pth.AgentHomePath(project, fmt.Sprintf("%d", i), repo)
				wpath := pth.AgentRepoPath(project, fmt.Sprintf("%d", i), repo)
				if i > len(names) {
					break
				}
				wcmd := fmt.Sprintf("docker exec -it %s bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd \"%s\"; exec bash'", names[i-1], whome, whome, whome, whome, whome, whome, whome, whome, whome, wpath)
				runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sess, fmt.Sprintf("agent-%d", i), wcmd)...)
			}
			// tmux attach is long-lived: no timeout
			runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sess)...)
		}
	case "bootstrap":
		// Opinionated: set up worktrees and open tmux with defaults if args omitted
		mustProject(project)
		if project != "dev-all" {
			die("Use -p dev-all for bootstrap")
		}
		var repo string
		var n int
		if len(sub) >= 2 {
			repo = sub[0]
			n = mustAtoi(sub[1])
		} else {
			// Try overlay defaults
			cfg, _ := config.ReadAll(paths.Root, project)
			if strings.TrimSpace(cfg.Defaults.Repo) == "" || cfg.Defaults.Agents < 1 {
				die("Usage: -p dev-all bootstrap <repo> <count> (or set defaults in overlays/dev-all/devkit.yaml)")
			}
			repo = cfg.Defaults.Repo
			n = cfg.Defaults.Agents
		}
		// Create worktrees and open tmux windows
		// Reuse this process: invoke internal handlers directly
		// Setup worktrees
		{
			// dev root path (parent of devkit)
			devRoot := filepath.Clean(filepath.Join(paths.Root, ".."))
			repoPath := filepath.Join(devRoot, repo)
			runner.Host(dryRun, "git", "-C", repoPath, "fetch", "--all", "--prune")
			runner.Host(dryRun, "git", "-C", repoPath, "config", "push.default", "upstream")
			runner.Host(dryRun, "git", "-C", repoPath, "config", "worktree.useRelativePaths", "true")
			base := "agent"
			baseBranch := "main"
			cfg, _ := config.ReadAll(paths.Root, project)
			if strings.TrimSpace(cfg.Defaults.BranchPrefix) != "" {
				base = cfg.Defaults.BranchPrefix
			}
			if strings.TrimSpace(cfg.Defaults.BaseBranch) != "" {
				baseBranch = cfg.Defaults.BaseBranch
			}
			br1 := fmt.Sprintf("%s1", base)
			// preserve local work for agent1 by not resetting to origin
			runner.Host(dryRun, "git", "-C", repoPath, "checkout", "-B", br1)
			runner.Host(dryRun, "git", "-C", repoPath, "branch", "--set-upstream-to=origin/"+baseBranch, br1)
			for i := 2; i <= n; i++ {
				parent := filepath.Join(devRoot, fmt.Sprintf("%s%d", base, i))
				if !dryRun {
					_ = os.MkdirAll(parent, 0o755)
				}
				wt := filepath.Join(parent, repo)
				bri := fmt.Sprintf("%s%d", base, i)
				runner.Host(dryRun, "git", "-C", repoPath, "worktree", "add", wt, "-B", bri, "origin/"+baseBranch)
				runner.Host(dryRun, "git", "-C", wt, "branch", "--set-upstream-to=origin/"+baseBranch, bri)
			}
		}
		// Bring up N agents and open tmux using existing worktrees-tmux behavior
		runner.Compose(dryRun, compose.AllProfilesFiles(paths, project), "up", "-d", "--scale", fmt.Sprintf("dev-agent=%d", n))
		// finally open tmux
		// call existing worktrees-tmux handler logic inline
		{
			base := "/workspaces/dev"
			sess := "devkit-worktrees"
			home1 := base + "/" + repo + "/.devhome-agent1"
			if !skipTmux() {
				cmd := "docker compose " + strings.Join(compose.AllProfilesFiles(paths, project), " ") + " exec --index 1 dev-agent bash -lc 'mkdir -p \"" + home1 + "/.codex/rollouts\" \"" + home1 + "/.cache\" \"" + home1 + "/.config\" \"" + home1 + "/.local\"; export HOME=\"" + home1 + "\"; export CODEX_HOME=\"" + home1 + "/.codex\"; export CODEX_ROLLOUT_DIR=\"" + home1 + "/.codex/rollouts\"; export XDG_CACHE_HOME=\"" + home1 + "/.cache\"; export XDG_CONFIG_HOME=\"" + home1 + "/.config\"; cd \"" + base + "/" + repo + "\"; exec bash'"
				runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
				runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
				for i := 2; i <= n; i++ {
					whome := fmt.Sprintf("%s/agent%d/.devhome-agent%d", base, i, i)
					wpath := fmt.Sprintf("%s/agent%d/%s", base, i, repo)
					wcmd := "docker compose " + strings.Join(compose.AllProfilesFiles(paths, project), " ") + fmt.Sprintf(" exec --index %d dev-agent bash -lc 'mkdir -p \"%s/.codex/rollouts\" \"%s/.cache\" \"%s/.config\" \"%s/.local\"; export HOME=\"%s\"; export CODEX_HOME=\"%s/.codex\"; export CODEX_ROLLOUT_DIR=\"%s/.codex/rollouts\"; export XDG_CACHE_HOME=\"%s/.cache\"; export XDG_CONFIG_HOME=\"%s/.config\"; cd \"%s\"; exec bash'", i, whome, whome, whome, whome, whome, whome, whome, whome, whome, wpath)
					runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sess, fmt.Sprintf("agent-%d", i), wcmd)...)
				}
				runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sess)...)
			}
		}
	default:
		usage()
		os.Exit(2)
	}
}

func die(msg string) { fmt.Fprintln(os.Stderr, msg); os.Exit(2) }
func mustProject(p string) {
	if strings.TrimSpace(p) == "" {
		die("-p <project> is required")
	}
}

func skipTmux() bool { return os.Getenv("DEVKIT_NO_TMUX") == "1" }
func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		die("count must be a positive integer")
	}
	return n
}

// defaultSessionName chooses a stable default tmux session per overlay.
func defaultSessionName(project string) string { return "devkit:" + project }

// hasTmuxSession returns true if the tmux session exists.
func hasTmuxSession(session string) bool {
	// Use Capture to avoid printing benign tmux errors like
	// "no server running on /tmp/tmux-<uid>/default" when probing.
	_, res := execx.Capture(context.Background(), "tmux", tmuxutil.HasSession(session)...)
	return res.Code == 0
}

// listTmuxWindows returns a set of window names for a session.
func listTmuxWindows(session string) map[string]struct{} {
	out, r := execx.Capture(context.Background(), "tmux", tmuxutil.ListWindows(session)...)
	if r.Code != 0 {
		return map[string]struct{}{}
	}
	m := map[string]struct{}{}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		s := strings.TrimSpace(ln)
		if s != "" {
			m[s] = struct{}{}
		}
	}
	return m
}

// buildWindowCmd composes the docker compose exec bash -lc command string for a given agent index and dest path.
func buildWindowCmd(fileArgs []string, project, idx, dest, service string) string {
	// Enforce git identity (no fallback): require name+email to be discoverable
	gname, gemail := gitIdentityFromHost()
	if strings.TrimSpace(gname) == "" || strings.TrimSpace(gemail) == "" {
		die("git identity not configured. Set DEVKIT_GIT_USER_NAME and DEVKIT_GIT_USER_EMAIL, or configure host git --global user.name/user.email")
	}
	// Anchor HOME that follows the container identity (index-free)
	anchor := anchorHome(project)
	// Build export sequence
	var b strings.Builder
	b.WriteString("set -e; ")
	base := anchorBase(project)
	anchorScripts := seed.BuildAnchorScripts(seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true})
	if joined := seed.JoinScripts(anchorScripts); joined != "" {
		b.WriteString(joined)
		b.WriteString("; ")
	}
	// exports
	fmt.Fprintf(&b, "export HOME=%q CODEX_HOME=%q CODEX_ROLLOUT_DIR=%q XDG_CACHE_HOME=%q XDG_CONFIG_HOME=%q SBT_GLOBAL_BASE=%q; ", anchor, filepath.Join(anchor, ".codex"), filepath.Join(anchor, ".codex", "rollouts"), filepath.Join(anchor, ".cache"), filepath.Join(anchor, ".config"), filepath.Join(anchor, ".sbt"))
	// ensure git identity inside container (explicit values to avoid relying on env injection)
	fmt.Fprintf(&b, "git config --global user.name %s && git config --global user.email %s; ", shSingleQuote(gname), shSingleQuote(gemail))
	// cd + exec bash
	fmt.Fprintf(&b, "cd %q 2>/dev/null || true; exec bash", dest)
	shell := b.String()
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	// Resolve container; poll briefly so windows appear even if service is still starting
	files := strings.Join(fileArgs, " ")
	find := fmt.Sprintf(
		"name=''; for i in $(seq 1 120); do "+
			"name=$(docker compose %s ps --format '{{.Name}}' %s | sed -n '%sp'); "+
			"if [ -n \"$name\" ]; then break; fi; "+
			"name=$(docker ps --filter label=com.docker.compose.service=%s --format '{{.Names}}' | sed -n '%sp'); "+
			"if [ -n \"$name\" ]; then break; fi; "+
			"name=$(docker compose %s ps --format '{{.Name}}' %s | sed -n '1p'); "+
			"if [ -n \"$name\" ]; then break; fi; "+
			"name=$(docker ps --filter label=com.docker.compose.service=%s --format '{{.Names}}' | sed -n '1p'); "+
			"if [ -n \"$name\" ]; then break; fi; "+
			"sleep 0.5; done; ",
		files, service, idx,
		service, idx,
		files, service,
		service)
	return find + "if [ -z \"$name\" ]; then echo 'No container for service " + service + " yet.'; exec bash; fi; " +
		"docker exec -it \"$name\" bash -lc '" + shell + "'"
}

// buildWindowCmdWithProject composes a docker exec that targets a specific compose project/service window.
func buildWindowCmdWithProject(fileArgs []string, project, idx, dest, service, composeProject string) string {
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	// Enforce git identity (no fallback)
	gname, gemail := gitIdentityFromHost()
	if strings.TrimSpace(gname) == "" || strings.TrimSpace(gemail) == "" {
		die("git identity not configured. Set DEVKIT_GIT_USER_NAME and DEVKIT_GIT_USER_EMAIL, or configure host git --global user.name/user.email")
	}
	var b strings.Builder
	b.WriteString("set -e; ")
	// Anchor HOME (index-free)
	anchor := anchorHome(project)
	base := anchorBase(project)
	anchorScripts := seed.BuildAnchorScripts(seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true})
	if joined := seed.JoinScripts(anchorScripts); joined != "" {
		b.WriteString(joined)
		b.WriteString("; ")
	}
	fmt.Fprintf(&b, "export HOME=%q CODEX_HOME=%q CODEX_ROLLOUT_DIR=%q XDG_CACHE_HOME=%q XDG_CONFIG_HOME=%q SBT_GLOBAL_BASE=%q; ", anchor, filepath.Join(anchor, ".codex"), filepath.Join(anchor, ".codex", "rollouts"), filepath.Join(anchor, ".cache"), filepath.Join(anchor, ".config"), filepath.Join(anchor, ".sbt"))
	fmt.Fprintf(&b, "git config --global user.name %s && git config --global user.email %s; ", shSingleQuote(gname), shSingleQuote(gemail))
	fmt.Fprintf(&b, "cd %q 2>/dev/null || true; exec bash", dest)
	shell := b.String()
	// When composeProject is provided, pick Nth container by labels to avoid file ambiguity
	if strings.TrimSpace(composeProject) == "" {
		return buildWindowCmd(fileArgs, project, idx, dest, service)
	}
	find := fmt.Sprintf(
		"name=''; for i in $(seq 1 120); do "+
			"name=$(docker ps --filter label=com.docker.compose.project=%s --filter label=com.docker.compose.service=%s --format '{{.Names}}' | sed -n '%sp'); "+
			"if [ -n \"$name\" ]; then break; fi; "+
			"name=$(docker ps --filter label=com.docker.compose.project=%s --filter label=com.docker.compose.service=%s --format '{{.Names}}' | sed -n '1p'); "+
			"if [ -n \"$name\" ]; then break; fi; "+
			"sleep 0.5; done; ",
		shSingleQuote(composeProject), shSingleQuote(service), idx,
		shSingleQuote(composeProject), shSingleQuote(service))
	return find + "if [ -z \"$name\" ]; then echo 'No container for " + composeProject + "/" + service + " yet.'; exec bash; fi; " +
		"docker exec -it \"$name\" bash -lc '" + shell + "'"
}

func runAnchorPlan(dry bool, files []string, service, idx string, cfg seed.AnchorConfig) {
	for _, script := range seed.BuildAnchorScripts(cfg) {
		execServiceIdx(dry, files, service, idx, script)
	}
}

// ensureTmuxSessionWithWindow ensures a session exists and adds a window for the given agent index and subpath.
func ensureTmuxSessionWithWindow(dry bool, paths compose.Paths, project string, fileArgs []string, session, idx, subpath, winName, service string) {
	if session == "" {
		session = defaultSessionName(project)
	}
	// compute dest path (relative paths under /workspaces/dev)
	dest := subpath
	if !strings.HasPrefix(subpath, "/") {
		dest = filepath.Join("/workspaces/dev", subpath)
	}
	if winName == "" {
		winName = "agent-" + idx
	}
	cmdStr := buildWindowCmd(fileArgs, project, idx, dest, service)
	if !hasTmuxSession(session) {
		// create new session with this window
		runner.Host(dry, "tmux", tmuxutil.NewSession(session, cmdStr)...)
		runner.Host(dry, "tmux", tmuxutil.RenameWindow(session+":0", winName)...)
		// attach interactively (short return if not interactive wanted)
		runner.HostInteractive(dry, "tmux", tmuxutil.Attach(session)...)
		return
	}
	// session exists: add a new window
	runner.Host(dry, "tmux", tmuxutil.NewWindow(session, winName, cmdStr)...)
}

// doSyncTmux ensures windows agent-1..count exist in the target session.
func doSyncTmux(dry bool, paths compose.Paths, project string, fileArgs []string, session, namePrefix, cdPath string, count int, service string) {
	if session == "" {
		session = defaultSessionName(project)
	}
	// Determine default cd path per overlay if not provided
	// For dev-all, base dir per agent; for codex, /workspace
	// We'll compute per-index below if cdPath empty
	present := map[string]struct{}{}
	sessExists := hasTmuxSession(session)
	if sessExists {
		present = listTmuxWindows(session)
	}
	// Create session if missing with first window
	start := 1
	if !sessExists {
		idx := "1"
		dest := cdPath
		if strings.TrimSpace(dest) == "" {
			dest = pth.AgentRepoPath(project, idx, "")
		}
		cmdStr := buildWindowCmd(fileArgs, project, idx, dest, service)
		runner.Host(dry, "tmux", tmuxutil.NewSession(session, cmdStr)...)
		runner.Host(dry, "tmux", tmuxutil.RenameWindow(session+":0", namePrefix+idx)...)
		present[namePrefix+idx] = struct{}{}
		start = 2
	}
	for i := start; i <= count; i++ {
		idx := fmt.Sprintf("%d", i)
		wname := namePrefix + idx
		if _, ok := present[wname]; ok {
			continue
		}
		dest := cdPath
		if strings.TrimSpace(dest) == "" {
			dest = pth.AgentRepoPath(project, idx, "")
		}
		cmdStr := buildWindowCmd(fileArgs, project, idx, dest, service)
		runner.Host(dry, "tmux", tmuxutil.NewWindow(session, wname, cmdStr)...)
	}
}

// rewriteWorktreeGitdir makes the .git file inside a worktree point to a relative gitdir
// so that it resolves correctly inside containers where the host absolute path differs.
func rewriteWorktreeGitdir(wt string) {
	// Resolve current gitdir
	out, res := execx.Capture(context.Background(), "git", "-C", wt, "rev-parse", "--git-dir")
	if res.Code != 0 {
		return
	}
	gitdir := strings.TrimSpace(out)
	if gitdir == "" {
		return
	}
	// Compute relative path from worktree dir to gitdir
	rel, err := filepath.Rel(wt, gitdir)
	if err != nil {
		return
	}
	// Write .git file with strict perms
	data := []byte("gitdir: " + rel + "\n")
	_ = os.WriteFile(filepath.Join(wt, ".git"), data, fs.FileMode(0644))
}
