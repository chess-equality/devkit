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

	agentexec "devkit/cli/devctl/internal/agentexec"
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

var tmuxForceOverride bool

func anchorHome(project string) string { return agentexec.AnchorHome(project) }

func composeProjectName(project string) string { return agentexec.ComposeProjectName(project) }

func defaultComposeProjectName(project string) string {
	p := strings.TrimSpace(project)
	if p == "" || p == "codex" {
		return "devkit"
	}
	return "devkit-" + p
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

func composeProjectOverride(project string) string {
	return strings.TrimSpace(os.Getenv(composeProjectEnvKey(project)))
}

func resolveComposeProjectName(project, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := composeProjectOverride(project); v != "" {
		return v
	}
	return defaultComposeProjectName(project)
}

func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func isNetworkConflictErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") || strings.Contains(msg, "pool overlaps with other one on this address space")
}

func pushNetworkEnv(cidr, dns string) func() {
	changed := map[string]*string{}
	set := func(key, val string) {
		if _, recorded := changed[key]; !recorded {
			if cur, ok := os.LookupEnv(key); ok {
				valCopy := cur
				changed[key] = &valCopy
			} else {
				changed[key] = nil
			}
		}
		_ = os.Setenv(key, val)
	}
	set("DEVKIT_INTERNAL_SUBNET", cidr)
	set("DEVKIT_DNS_IP", dns)
	return func() {
		for key, prev := range changed {
			if prev == nil {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, *prev)
			}
		}
	}
}

func pickOverlaySubnet(netCfg *layout.Network, tried map[string]bool, global map[string]bool) (string, string, error) {
	preferred := ""
	if netCfg != nil {
		preferred = strings.TrimSpace(netCfg.Subnet)
	}
	used := append([]string{}, netutil.UsedCIDRs()...)
	for cid := range global {
		used = append(used, cid)
	}
	for cid := range tried {
		used = append(used, cid)
	}
	for _, candidate := range candidateCIDRs(preferred) {
		if tried[candidate] {
			continue
		}
		if netutil.OverlapsAnyCIDR(candidate, used) {
			continue
		}
		dns := ""
		if netCfg != nil && strings.TrimSpace(netCfg.Subnet) == candidate {
			dns = strings.TrimSpace(netCfg.DNSIP)
		}
		if dns == "" {
			dns = netutil.DNSFromCIDR(candidate)
		}
		return candidate, dns, nil
	}
	return "", "", fmt.Errorf("layout-apply: unable to find free subnet (preferred %q)", preferred)
}

func candidateCIDRs(preferred string) []string {
	seen := map[string]bool{}
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
	}
	add(preferred)
	for second := 16; second <= 31; second++ {
		for third := 0; third < 256; third += 8 {
			add(fmt.Sprintf("172.%d.%d.0/24", second, third))
		}
	}
	for second := 0; second < 256; second += 8 {
		for third := 0; third < 256; third += 16 {
			add(fmt.Sprintf("10.%d.%d.0/24", second, third))
		}
	}
	for third := 0; third < 256; third += 4 {
		add(fmt.Sprintf("192.168.%d.0/24", third))
	}
	result := make([]string, 0, len(seen))
	for cid := range seen {
		result = append(result, cid)
	}
	sort.Strings(result)
	// Ensure preferred (if present) remains first.
	if preferred != "" && seen[strings.TrimSpace(preferred)] {
		for i, cid := range result {
			if cid == strings.TrimSpace(preferred) {
				if i != 0 {
					result[0], result[i] = result[i], result[0]
				}
				break
			}
		}
	}
	return result
}

func withComposeProject(name string) func() {
	name = strings.TrimSpace(name)
	prev, had := os.LookupEnv("COMPOSE_PROJECT_NAME")
	if name == "" {
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

func anchorBase(project string) string { return agentexec.AnchorBase(project) }

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

func listServiceNamesProject(project, service string) []string {
	project = strings.TrimSpace(project)
	ctx, cancel := execx.WithTimeout(30 * time.Second)
	defer cancel()
	if project == "" {
		return listServiceNamesAny(service)
	}
	args := []string{"ps", "--filter", "label=com.docker.compose.project=" + project}
	if strings.TrimSpace(service) != "" {
		args = append(args, "--filter", "label=com.docker.compose.service="+service)
	}
	args = append(args, "--format", "{{.Names}}")
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

func applyOverlayEnv(cfg config.OverlayConfig, overlayDir string, root string) {
	applyOverlayEnvInternal(cfg, overlayDir, root, false, nil)
}

func pushOverlayEnv(cfg config.OverlayConfig, overlayDir string, root string, force bool) func() {
	changed := map[string]*string{}
	applyOverlayEnvInternal(cfg, overlayDir, root, force, changed)
	if len(changed) == 0 {
		return nil
	}
	return func() {
		keys := make([]string, 0, len(changed))
		for k := range changed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			prev := changed[key]
			if prev == nil {
				_ = os.Unsetenv(key)
				continue
			}
			_ = os.Setenv(key, *prev)
		}
	}
}

func pushEnvMap(env map[string]string) func() {
	if len(env) == 0 {
		return nil
	}
	type pair struct {
		key string
		val string
	}
	pairs := make([]pair, 0, len(env))
	for k, v := range env {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		pairs = append(pairs, pair{key: key, val: v})
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].key < pairs[j].key
	})
	changed := map[string]*string{}
	for _, p := range pairs {
		if _, recorded := changed[p.key]; !recorded {
			if cur, ok := os.LookupEnv(p.key); ok {
				val := cur
				changed[p.key] = &val
			} else {
				changed[p.key] = nil
			}
		}
		_ = os.Setenv(p.key, expandValue(p.val))
	}
	return func() {
		for i := len(pairs) - 1; i >= 0; i-- {
			key := pairs[i].key
			prev := changed[key]
			if prev == nil {
				_ = os.Unsetenv(key)
				continue
			}
			_ = os.Setenv(key, *prev)
		}
	}
}

func combineRestorers(restorers ...func()) func() {
	active := make([]func(), 0, len(restorers))
	for _, fn := range restorers {
		if fn != nil {
			active = append(active, fn)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func() {
		for i := len(active) - 1; i >= 0; i-- {
			active[i]()
		}
	}
}

func applyOverlayEnvInternal(cfg config.OverlayConfig, overlayDir string, root string, force bool, changed map[string]*string) {
	base := strings.TrimSpace(overlayDir)
	if base == "" {
		base = root
	}
	if _, ok := os.LookupEnv("DEVKIT_WORKTREE_CONTAINER_ROOT"); !ok {
		_ = os.Setenv("DEVKIT_WORKTREE_CONTAINER_ROOT", "/worktrees")
	}
	if _, ok := os.LookupEnv("DEVKIT_WORKTREE_ROOT"); !ok {
		defaultRoot := filepath.Join(filepath.Clean(filepath.Join(root, "..")), pth.AgentWorktreesDir)
		if strings.TrimSpace(defaultRoot) != "" {
			_ = os.Setenv("DEVKIT_WORKTREE_ROOT", defaultRoot)
		}
	}
	setEnv := func(key string, raw string, resolved string, expand bool) {
		k := strings.TrimSpace(key)
		if k == "" {
			return
		}
		if !force && !shouldSetEnv(k, raw) {
			return
		}
		if changed != nil {
			if _, recorded := changed[k]; !recorded {
				if cur, ok := os.LookupEnv(k); ok {
					val := cur
					changed[k] = &val
				} else {
					changed[k] = nil
				}
			}
		}
		val := resolved
		if expand {
			val = expandValue(resolved)
		}
		_ = os.Setenv(k, val)
	}
	ws := strings.TrimSpace(cfg.Workspace)
	if ws != "" {
		resolved := ws
		if !filepath.IsAbs(ws) {
			resolved = filepath.Clean(filepath.Join(base, ws))
		}
		setEnv("WORKSPACE_DIR", cfg.Workspace, resolved, false)
	}
	for _, file := range cfg.EnvFiles {
		path := strings.TrimSpace(file)
		if path == "" {
			continue
		}
		resolved := path
		if !filepath.IsAbs(path) {
			resolved = filepath.Join(base, path)
		}
		for k, v := range readEnvFile(resolved) {
			setEnv(k, v, v, true)
		}
	}
	for key, val := range cfg.Env {
		setEnv(key, val, val, true)
	}
}

func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		parts := strings.SplitN(trim, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func resolveHostOverlayPaths(raw []string, baseDir string, root string) []string {
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		trim := strings.TrimSpace(p)
		if trim == "" {
			continue
		}
		resolved := expandHome(trim)
		if !filepath.IsAbs(resolved) {
			anchor := root
			if baseDir != "" {
				anchor = baseDir
			}
			resolved = filepath.Join(anchor, resolved)
		}
		out = append(out, filepath.Clean(resolved))
	}
	return out
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		house, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(house, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func shouldSetEnv(key, value string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	cur, exists := os.LookupEnv(key)
	if !exists || cur == "" {
		return true
	}
	needle1 := "$" + key
	needle2 := "${" + key + "}"
	return strings.Contains(value, needle1) || strings.Contains(value, needle2)
}

func expandValue(raw string) string {
	return os.Expand(raw, func(name string) string {
		return os.Getenv(name)
	})
}

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

func resolveServiceContainer(files []string, service, idx string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		names := listServiceNames(files, service)
		if len(names) == 0 {
			names = listServiceNamesAny(service)
		}
		name := pickByIndex(names, idx)
		if strings.TrimSpace(name) != "" {
			return name
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func guessServiceContainerName(files []string, service, idx string) string {
	project := guessComposeProjectName(files)
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	n := 1
	if v, err := strconv.Atoi(idx); err == nil && v >= 1 {
		n = v
	}
	return fmt.Sprintf("%s-%s-%d", project, service, n)
}

func guessComposeProjectName(files []string) string {
	for i := 0; i < len(files)-1; i++ {
		if files[i] != "-f" {
			continue
		}
		path := filepath.ToSlash(files[i+1])
		const marker = "/overlays/"
		idx := strings.Index(path, marker)
		if idx < 0 {
			continue
		}
		remainder := path[idx+len(marker):]
		parts := strings.SplitN(remainder, "/", 2)
		if len(parts) == 0 {
			continue
		}
		overlay := strings.TrimSpace(parts[0])
		if overlay == "" {
			continue
		}
		name := sanitizeOverlayName(overlay)
		if name == "" {
			name = overlay
		}
		return "devkit-" + name
	}
	return "devkit"
}

func sanitizeOverlayName(raw string) string {
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
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
			placeholder := guessServiceContainerName(files, "dev-agent", idx)
			if strings.TrimSpace(placeholder) != "" {
				runner.Host(dry, "docker", "exec", "-t", placeholder, "bash", "-lc", script)
			}
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
	if dry {
		names := listServiceNames(files, service)
		if len(names) == 0 {
			names = listServiceNamesAny(service)
		}
		name := pickByIndex(names, idx)
		if strings.TrimSpace(name) == "" {
			runner.Compose(dry, files, "exec", "--index", idx, service, "bash", "-lc", script)
			placeholder := guessServiceContainerName(files, service, idx)
			if strings.TrimSpace(placeholder) != "" {
				runner.Host(dry, "docker", "exec", "-t", placeholder, "bash", "-lc", script)
			}
			return
		}
		runner.Host(dry, "docker", "exec", "-t", name, "bash", "-lc", script)
		return
	}
	name := resolveServiceContainer(files, service, idx, 60*time.Second)
	if strings.TrimSpace(name) == "" {
		die(service + " not running")
	}
	runner.Host(dry, "docker", "exec", "-t", name, "bash", "-lc", script)
}

// execServiceIdxInput runs a bash script with stdin content inside the Nth container for the given service.
func execServiceIdxInput(dry bool, files []string, service, idx string, input []byte, script string) {
	if strings.TrimSpace(service) == "" {
		service = "dev-agent"
	}
	if dry {
		runner.Compose(dry, files, "exec", "--index", idx, service, "bash", "-lc", script)
		return
	}
	name := resolveServiceContainer(files, service, idx, 60*time.Second)
	if strings.TrimSpace(name) == "" {
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
	if dry {
		runner.Compose(dry, files, append([]string{"exec", "-T", "--index", idx, service}, argv...)...)
		return
	}
	name := resolveServiceContainer(files, service, idx, 60*time.Second)
	if strings.TrimSpace(name) == "" {
		die(service + " not running")
	}
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
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
	if dry {
		runner.ComposeInteractive(dry, files, "exec", "--index", idx, service, "bash", "-lc", bashCmd)
		return
	}
	name := resolveServiceContainer(files, service, idx, 60*time.Second)
	if strings.TrimSpace(name) == "" {
		die(service + " not running")
	}
	runner.HostInteractive(dry, "docker", "exec", "-it", name, "bash", "-lc", bashCmd)
}

// resolveService returns the default service for a project overlay, falling back to dev-agent.
func resolveService(project string, overlayPaths []string) string {
	svc := "dev-agent"
	if strings.TrimSpace(project) == "" {
		return svc
	}
	if cfg, _, err := config.ReadAll(overlayPaths, project); err == nil {
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
  layout-validate --file <layout.yaml>                (static checks; exits non-zero on errors)
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
  --compose-project override compose project name for this invocation
  --tmux          force tmux integration even if DEVKIT_NO_TMUX=1

Environment:
  DEVKIT_DEBUG=1  print executed commands
`)
}

func main() {
	var project string
	var profile string
	var composeProject string
	var dryRun bool
	var noTmux bool
	var forceTmux bool
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
		case "--compose-project":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--compose-project requires value")
				os.Exit(2)
			}
			composeProject = args[i+1]
			i++
		case "--dry-run":
			dryRun = true
		case "--no-tmux":
			noTmux = true
		case "--tmux":
			forceTmux = true
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
	if strings.TrimSpace(composeProject) != "" {
		composeProject = strings.TrimSpace(composeProject)
		_ = os.Setenv("COMPOSE_PROJECT_NAME", composeProject)
		if strings.TrimSpace(project) != "" {
			_ = os.Setenv(composeProjectEnvKey(project), composeProject)
		}
	}

	exe, _ := os.Executable()
	paths, _ := compose.DetectPathsFromExe(exe)
	hostCfg, hostCfgDir, hostErr := config.ReadHostConfig()
	if hostErr != nil {
		fmt.Fprintf(os.Stderr, "[devctl] warning: failed to parse host config: %v\n", hostErr)
	}
	if url := strings.TrimSpace(hostCfg.CLI.DownloadURL); url != "" {
		if _, ok := os.LookupEnv("DEVKIT_CLI_DOWNLOAD_URL"); !ok {
			_ = os.Setenv("DEVKIT_CLI_DOWNLOAD_URL", url)
		}
	}
	for key, val := range hostCfg.Env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, ok := os.LookupEnv(key); ok {
			continue
		}
		_ = os.Setenv(key, val)
	}
	if strings.TrimSpace(composeProject) == "" {
		if _, ok := os.LookupEnv("COMPOSE_PROJECT_NAME"); !ok {
			if v := composeProjectOverride(project); v != "" {
				_ = os.Setenv("COMPOSE_PROJECT_NAME", v)
			}
		}
	}
	if len(hostCfg.OverlayPaths) > 0 {
		extra := resolveHostOverlayPaths(hostCfg.OverlayPaths, hostCfgDir, paths.Root)
		paths.OverlayPaths = compose.MergeOverlayPaths(paths.OverlayPaths, extra...)
	}
	overlayDir := compose.FindOverlayDir(paths.OverlayPaths, project)
	overlayCfg, cfgDir, cfgErr := config.ReadAll(paths.OverlayPaths, project)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "[devctl] warning: failed to parse devkit.yaml for %s: %v\n", project, cfgErr)
	}
	if cfgDir == "" {
		cfgDir = overlayDir
	}
	applyOverlayEnv(overlayCfg, cfgDir, paths.Root)
	tmuxForceOverride = forceTmux
	if forceTmux {
		_ = os.Unsetenv("DEVKIT_NO_TMUX")
	}
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
	tmuxcmd.Register(registry, doSyncTmux, ensureTmuxSessionWithWindow, defaultSessionName, mustAtoi, listServiceNames, buildWindowCmd, agentexec.NewSeedTracker, hasTmuxSession)
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
		if cmd == "up" {
			pname := composeProjectName(ctx.Project)
			composecmd.CleanupSharedInfra(ctx.DryRun, pname, ctx.Files)
			if err := handler(ctx); err != nil {
				die(err.Error())
			}
			seedAfterUp(ctx, pname)
			return
		}
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
		type overlayRun struct {
			Project     string
			Service     string
			Count       int
			Build       bool
			Worktrees   *layout.Worktrees
			Network     *layout.Network
			ComposeProj string
			FileArgs    []string
			Env         map[string]string
			OverlayCfg  config.OverlayConfig
			OverlayDir  string
		}
		var overlayRuns []overlayRun
		projMap := map[string]string{}
		filesByProject := map[string][]string{}
		if strings.TrimSpace(project) != "" {
			filesByProject[project] = files
		}
		for _, ov := range lf.Overlays {
			ovProj := strings.TrimSpace(ov.Project)
			if ovProj == "" {
				continue
			}
			ovCfg, ovDir, ovErr := config.ReadAll(paths.OverlayPaths, ovProj)
			if ovErr != nil {
				die(ovErr.Error())
			}
			pname := resolveComposeProjectName(ovProj, ov.ComposeProject)
			restoreProj := withComposeProject(pname)
			filesOv, err := compose.Files(paths, ovProj, ov.Profiles)
			if restoreProj != nil {
				restoreProj()
			}
			if err != nil {
				die(err.Error())
			}
			projMap[ovProj] = pname
			var envCopy map[string]string
			if len(ov.Env) > 0 {
				envCopy = make(map[string]string, len(ov.Env))
				for k, v := range ov.Env {
					envCopy[k] = v
				}
			}
			run := overlayRun{
				Project:     ovProj,
				Service:     ov.Service,
				Count:       ov.Count,
				Build:       ov.Build,
				Worktrees:   ov.Worktrees,
				Network:     ov.Network,
				ComposeProj: pname,
				FileArgs:    filesOv,
				Env:         envCopy,
				OverlayCfg:  ovCfg,
				OverlayDir:  ovDir,
			}
			if os.Getenv("DEVKIT_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "[layout] register overlay %s compose_project=%s profiles=%s\n", run.Project, run.ComposeProj, strings.TrimSpace(ov.Profiles))
			}
			filesByProject[ovProj] = filesOv
			overlayRuns = append(overlayRuns, run)
		}
		allocatedCIDRs := map[string]bool{}
		resolveWindowFiles := func(winProj string) []string {
			if fargs, ok := filesByProject[winProj]; ok && len(fargs) > 0 {
				return fargs
			}
			fargs, err := compose.Files(paths, winProj, profile)
			if err != nil {
				die(err.Error())
			}
			filesByProject[winProj] = fargs
			return fargs
		}
		// 0) Optional: prepare host-side worktrees for dev-all overlays that request it
		for _, run := range overlayRuns {
			if strings.TrimSpace(run.Project) != "dev-all" {
				continue
			}
			if run.Worktrees == nil {
				continue
			}
			repo := strings.TrimSpace(run.Worktrees.Repo)
			if repo == "" {
				continue
			}
			restoreProj := withComposeProject(run.ComposeProj)
			count := run.Worktrees.Count
			if count <= 0 {
				count = run.Count
			}
			if count <= 0 {
				count = 1
			}
			baseBranch := strings.TrimSpace(run.Worktrees.BaseBranch)
			branchPrefix := strings.TrimSpace(run.Worktrees.BranchPrefix)
			if baseBranch == "" || branchPrefix == "" {
				if cfg, _, er := config.ReadAll(paths.OverlayPaths, "dev-all"); er == nil {
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
				if restoreProj != nil {
					restoreProj()
				}
				die("worktrees setup failed: " + err.Error())
			}
			if restoreProj != nil {
				restoreProj()
			}
		}
		// 0.5) Track which compose projects have been cleaned to avoid stale shared containers across overlays.
		cleanedProjects := map[string]bool{}
		withNetwork := func(run overlayRun, fn func(cidr, dns string) error) error {
			tried := map[string]bool{}
			var lastErr error
			for attempt := 0; attempt < 64; attempt++ {
				cidr, dns, pickErr := pickOverlaySubnet(run.Network, tried, allocatedCIDRs)
				if pickErr != nil {
					lastErr = pickErr
					break
				}
				tried[cidr] = true
				restoreNet := pushNetworkEnv(cidr, dns)
				err := fn(cidr, dns)
				restoreNet()
				if err == nil {
					allocatedCIDRs[cidr] = true
					return nil
				}
				lastErr = err
				if !isNetworkConflictErr(err) {
					break
				}
				composecmd.CleanupSharedInfra(dryRun, run.ComposeProj, run.FileArgs)
			}
			if lastErr != nil {
				return lastErr
			}
			return fmt.Errorf("layout-apply: failed to allocate network for overlay %s", run.Project)
		}
		// 1) Bring up overlays with their own profiles and project names
		tracker := agentexec.NewSeedTracker()
		for _, run := range overlayRuns {
			ovProj := run.Project
			restoreEnv := combineRestorers(
				pushOverlayEnv(run.OverlayCfg, run.OverlayDir, paths.Root, true),
				pushEnvMap(run.Env),
			)
			svc := run.Service
			if strings.TrimSpace(svc) == "" {
				svc = "dev-agent"
			}
			cnt := run.Count
			if cnt < 1 {
				cnt = 1
			}
			pname := run.ComposeProj
			restoreProj := withComposeProject(pname)
			if !cleanedProjects[pname] {
				composecmd.CleanupSharedInfra(dryRun, pname, run.FileArgs)
				cleanedProjects[pname] = true
			}
			err := withNetwork(run, func(cidr, dns string) error {
				prepareComposeProjectVolumes(dryRun, pname)
				args := []string{"up", "-d", "--scale", fmt.Sprintf("%s=%d", svc, cnt)}
				if run.Build {
					args = append(args, "--build")
				}
				if os.Getenv("DEVKIT_DEBUG") == "1" {
					fmt.Fprintf(os.Stderr, "[layout] overlay %s using subnet %s dns %s\n", ovProj, cidr, dns)
				}
				if err := runner.ComposeWithProject(dryRun, pname, run.FileArgs, args...); err != nil {
					return err
				}
				prepareComposeProjectVolumes(dryRun, pname)
				if !cleanedProjects[pname] {
					composecmd.CleanupSharedInfra(dryRun, pname, run.FileArgs)
					cleanedProjects[pname] = true
				}
				return nil
			})
			if err != nil {
				if restoreEnv != nil {
					restoreEnv()
				}
				if restoreProj != nil {
					restoreProj()
				}
				die(err.Error())
			}
			if restoreEnv != nil {
				restoreEnv()
			}
			if restoreProj != nil {
				restoreProj()
			}
		}
		// 1b) Ensure per-overlay SSH/Git is ready (anchor HOME + keys + global sshCommand)
		for _, run := range overlayRuns {
			restoreEnv := combineRestorers(
				pushOverlayEnv(run.OverlayCfg, run.OverlayDir, paths.Root, true),
				pushEnvMap(run.Env),
			)
			filesOv := run.FileArgs
			svc := run.Service
			if strings.TrimSpace(svc) == "" {
				svc = resolveService(run.Project, paths.OverlayPaths)
			} else {
				svc = strings.TrimSpace(svc)
			}
			if svc == "" {
				if restoreEnv != nil {
					restoreEnv()
				}
				continue
			}
			cnt := run.Count
			if cnt < 1 {
				cnt = 1
			}
			restoreProj := withComposeProject(run.ComposeProj)
			configureSSHAndGit(dryRun, filesOv, run.Project, svc, cnt)
			if restoreEnv != nil {
				restoreEnv()
			}
			if restoreProj != nil {
				restoreProj()
			}
		}
		// 2) Apply windows into tmux using the composed project names
		if skipTmux() {
			fmt.Fprintln(os.Stderr, "[layout] tmux skipped via DEVKIT_NO_TMUX")
		} else {
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
				fargs := resolveWindowFiles(winProj)
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
					pname = agentexec.ComposeProjectName(winProj)
				}
				idxInt, _ := strconv.Atoi(idx)
				if idxInt < 1 {
					idxInt = 1
				}
				containerName := ""
				if !dryRun {
					containerName = agentexec.ResolveContainerName(pname, svc, idxInt)
				}
				cmdStr, err := buildWindowCmdForProject(fargs, winProj, idx, dest, svc, pname, containerName, tracker)
				if err != nil {
					die(err.Error())
				}
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
				fargs := resolveWindowFiles(winProj)
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
					pname = agentexec.ComposeProjectName(winProj)
				}
				idxInt, _ := strconv.Atoi(idx)
				if idxInt < 1 {
					idxInt = 1
				}
				containerName := ""
				if !dryRun {
					containerName = agentexec.ResolveContainerName(pname, svc, idxInt)
				}
				cmdStr, err := buildWindowCmdForProject(fargs, winProj, idx, dest, svc, pname, containerName, tracker)
				if err != nil {
					die(err.Error())
				}
				runner.Host(dryRun, "tmux", tmuxutil.NewWindow(sessName, name, cmdStr)...)
			}
			if doAttach {
				switch {
				case !stdoutIsTTY():
					fmt.Fprintln(os.Stderr, "layout-apply: --attach skipped because stdout is not a TTY")
				default:
					runner.HostInteractive(dryRun, "tmux", tmuxutil.Attach(sessName)...)
				}
			}
		}
	case "layout-validate":
		layoutPath := ""
		for i := 0; i < len(sub); i++ {
			if sub[i] == "--file" && i+1 < len(sub) {
				layoutPath = sub[i+1]
				i++
			}
		}
		if strings.TrimSpace(layoutPath) == "" {
			die("Usage: layout-validate --file <layout.yaml>")
		}
		lf, err := layout.Read(layoutPath)
		if err != nil {
			die(err.Error())
		}
		warns, errs := layout.Validate(lf, project)
		for _, msg := range warns {
			fmt.Println("[warn]", msg)
		}
		if len(errs) > 0 {
			for _, msg := range errs {
				fmt.Fprintln(os.Stderr, "[error]", msg)
			}
			os.Exit(2)
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
		svc := resolveService(project, paths.OverlayPaths)
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
		svc := resolveService(project, paths.OverlayPaths)
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
			if cfg, _, err := config.ReadAll(paths.OverlayPaths, project); err == nil {
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
	case "doctor-runtime":
		mustProject(project)
		if os.Getenv("DEVKIT_ENABLE_RUNTIME_CONFIG") != "1" {
			die("runtime config disabled; set DEVKIT_ENABLE_RUNTIME_CONFIG=1")
		}
		root := strings.TrimSpace(os.Getenv("DEVKIT_WORKTREE_ROOT"))
		if root == "" {
			die("DEVKIT_WORKTREE_ROOT not set")
		}
		if !filepath.IsAbs(root) {
			die("DEVKIT_WORKTREE_ROOT must be an absolute path")
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			if err != nil {
				die("worktree root not accessible: " + err.Error())
			}
			die("worktree root is not a directory: " + root)
		}
		svc := resolveService(project, paths.OverlayPaths)
		if strings.TrimSpace(svc) == "" {
			svc = "dev-agent"
		}
		if dryRun {
			fmt.Printf("[doctor-runtime] would inspect service %s containers for /worktrees mount\n", svc)
			runner.Compose(dryRun, files, "exec", "--index", "1", svc, "bash", "-lc", "test -d /worktrees && mount | grep ' /worktrees '")
			break
		}
		names := listServiceNames(files, svc)
		if len(names) == 0 {
			die("no containers for service " + svc + "; bring the overlay up first")
		}
		fmt.Printf("doctor-runtime: host worktree root %s OK\n", root)
		for idx, name := range names {
			index := fmt.Sprintf("%d", idx+1)
			script := "set -euo pipefail; test -d /worktrees; mount | grep -q ' /worktrees '"
			runner.Compose(dryRun, files, "exec", "--index", index, svc, "bash", "-lc", script)
			fmt.Printf("container %s mounts /worktrees\n", name)
		}
		fmt.Println("doctor-runtime: OK")
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
				cfg, _, _ := config.ReadAll(paths.OverlayPaths, project)
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
		svc := resolveService(project, paths.OverlayPaths)
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
		svc := resolveService(project, paths.OverlayPaths)
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
		removeLegacySharedContainers(dryRun)
		removeProjectNetworks(dryRun, composeProjectName(project))
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
			tracker := agentexec.NewSeedTracker()
			cmd := mustBuildWindowCmd(all, project, "1", "/workspace", "dev-agent", tracker)
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n; i++ {
				wcmd := mustBuildWindowCmd(all, project, fmt.Sprintf("%d", i), "/workspace", "dev-agent", tracker)
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
		removeLegacySharedContainers(dryRun)
		removeProjectNetworks(dryRun, composeProjectName(project))
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
			tracker := agentexec.NewSeedTracker()
			cmd := mustBuildWindowCmd(all, project, "1", "/workspace", "dev-agent", tracker)
			runner.Host(dryRun, "tmux", tmuxutil.NewSession(sess, cmd)...)
			runner.Host(dryRun, "tmux", tmuxutil.RenameWindow(sess+":0", "agent-1")...)
			for i := 2; i <= n; i++ {
				wcmd := mustBuildWindowCmd(all, project, fmt.Sprintf("%d", i), "/workspace", "dev-agent", tracker)
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
			if cfg, _, err := config.ReadAll(paths.OverlayPaths, project); err == nil && strings.TrimSpace(cfg.Defaults.Repo) != "" {
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
			svc := resolveService(project, paths.OverlayPaths)
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
			svc := resolveService(project, paths.OverlayPaths)
			execServiceIdxInput(dryRun, files, svc, idx, step.Content, step.Script)
		}
		if hasRsa {
			svc := resolveService(project, paths.OverlayPaths)
			// write id_rsa with 600 perms
			execServiceIdxInput(dryRun, files, svc, idx, rsaBytes, "cat > '"+anchor+"'/.ssh/id_rsa && chmod 600 '"+anchor+"'/.ssh/id_rsa")
		}
		// git config global sshCommand and repo-level sshCommand, then validate with a pull
		{
			repoPath := pth.AgentRepoPath(project, idx, repoName)
			for _, sc := range sshw.BuildConfigureScripts(anchor, repoPath) {
				svc := resolveService(project, paths.OverlayPaths)
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
			svc := resolveService(project, paths.OverlayPaths)
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
			svc := resolveService(project, paths.OverlayPaths)
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
			svc := resolveService(project, paths.OverlayPaths)
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
			svc := resolveService(project, paths.OverlayPaths)
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
			svc := resolveService(project, paths.OverlayPaths)
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
		// (prevents mismatches when a previous compose project network used different IPAM settings)
		// 1) Best-effort compose down with all profiles for this project to stop attached containers
		all := compose.AllProfilesFiles(paths, project)
		runner.Compose(dryRun, all, "down")
		// 2) Remove known sidecars and any lingering dev-agents
		removeLegacySharedContainers(dryRun)
		// Remove any devkit-dev-agent-* containers that may still be around and attached
		runner.HostBestEffort(dryRun, "bash", "-lc", "docker ps -aq --filter name='^devkit-dev-agent-' | xargs -r docker rm -f")
		// 3) Now remove networks (will succeed once no active endpoints remain)
		removeProjectNetworks(dryRun, composeProjectName(project))
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
			cfg, _, _ := config.ReadAll(paths.OverlayPaths, project)
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
			cfg, _, _ := config.ReadAll(paths.OverlayPaths, project)
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

func removeLegacySharedContainers(dry bool) {
	runner.HostBestEffort(dry, "docker", "rm", "-f", "devkit_envoy", "devkit_envoy_sni", "devkit_dns", "devkit_tinyproxy")
}

func removeProjectNetworks(dry bool, composeProject string) {
	proj := strings.TrimSpace(composeProject)
	if proj == "" {
		proj = "devkit"
	}
	nets := []string{fmt.Sprintf("%s_dev-internal", proj), fmt.Sprintf("%s_dev-egress", proj)}
	for _, net := range nets {
		runner.HostBestEffort(dry, "docker", "network", "rm", net)
	}
}

func skipTmux() bool {
	if tmuxForceOverride {
		return false
	}
	return os.Getenv("DEVKIT_NO_TMUX") == "1"
}
func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		die("count must be a positive integer")
	}
	return n
}

// defaultSessionName chooses a stable default tmux session per overlay.
func defaultSessionName(project string) string {
	p := strings.TrimSpace(project)
	if p == "" {
		p = "layout"
	}
	return "devkit:" + p
}

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

// buildWindowCmd composes the docker exec command for a given agent index and dest path using shared agentexec logic.
func buildWindowCmd(fileArgs []string, project, idx, dest, service, composeProject string, tracker *agentexec.SeedTracker) (string, error) {
	return buildWindowCmdForProject(fileArgs, project, idx, dest, service, composeProject, "", tracker)
}

func buildWindowCmdForProject(fileArgs []string, project, idx, dest, service, composeProject, containerName string, tracker *agentexec.SeedTracker) (string, error) {
	gname, gemail := gitIdentityFromHost()
	if strings.TrimSpace(gname) == "" || strings.TrimSpace(gemail) == "" {
		return "", fmt.Errorf("git identity not configured. Set DEVKIT_GIT_USER_NAME and DEVKIT_GIT_USER_EMAIL, or configure host git --global user.name/user.email")
	}
	return agentexec.BuildCommand(agentexec.CommandOpts{
		Files:          fileArgs,
		Project:        project,
		Index:          idx,
		Dest:           dest,
		Service:        service,
		ComposeProject: composeProject,
		ContainerName:  containerName,
		Tracker:        tracker,
		GitName:        gname,
		GitEmail:       gemail,
	})
}

func mustBuildWindowCmd(fileArgs []string, project, idx, dest, service string, tracker *agentexec.SeedTracker) string {
	cmd, err := buildWindowCmd(fileArgs, project, idx, dest, service, "", tracker)
	if err != nil {
		die(err.Error())
	}
	return cmd
}

func mustBuildWindowCmdForProject(fileArgs []string, project, idx, dest, service, composeProject, containerName string, tracker *agentexec.SeedTracker) string {
	cmd, err := buildWindowCmdForProject(fileArgs, project, idx, dest, service, composeProject, containerName, tracker)
	if err != nil {
		die(err.Error())
	}
	return cmd
}

func runAnchorPlan(dry bool, files []string, service, idx string, cfg seed.AnchorConfig) {
	for _, script := range seed.BuildAnchorScripts(cfg) {
		execServiceIdx(dry, files, service, idx, script)
	}
}

func seedAfterUp(ctx *cmdregistry.Context, projectName string) {
	if ctx.DryRun {
		return
	}
	svc := resolveService(ctx.Project, ctx.Paths.OverlayPaths)
	if strings.TrimSpace(svc) == "" {
		svc = "dev-agent"
	}
	names := listServiceNamesProject(projectName, svc)
	if len(names) == 0 {
		names = listServiceNames(ctx.Files, svc)
	}
	cnt := len(names)
	if cnt == 0 {
		return
	}
	configureSSHAndGit(ctx.DryRun, ctx.Files, ctx.Project, svc, cnt)
}

func configureSSHAndGit(dry bool, files []string, project string, service string, count int) {
	hostEd := filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
	hostRsa := filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
	edBytes, _ := os.ReadFile(hostEd)
	rsaBytes, _ := os.ReadFile(hostRsa)
	known := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
	knownBytes, _ := os.ReadFile(known)
	for i := 1; i <= count; i++ {
		idx := fmt.Sprintf("%d", i)
		base := anchorBase(project)
		anchor := anchorHome(project)
		runAnchorPlan(dry, files, service, idx, seed.AnchorConfig{Anchor: anchor, Base: base, SeedCodex: true})
		cfg := sshcfg.BuildGitHubConfigFor(anchor, len(edBytes) > 0, len(rsaBytes) > 0)
		if len(edBytes) > 0 {
			execServiceIdxInput(dry, files, service, idx, edBytes, "cat > '"+anchor+"'/.ssh/id_ed25519 && chmod 600 '"+anchor+"'/.ssh/id_ed25519")
			pubBytes, _ := os.ReadFile(hostEd + ".pub")
			if len(pubBytes) > 0 {
				execServiceIdxInput(dry, files, service, idx, pubBytes, "cat > '"+anchor+"'/.ssh/id_ed25519.pub && chmod 644 '"+anchor+"'/.ssh/id_ed25519.pub")
			}
		}
		if len(rsaBytes) > 0 {
			execServiceIdxInput(dry, files, service, idx, rsaBytes, "cat > '"+anchor+"'/.ssh/id_rsa && chmod 600 '"+anchor+"'/.ssh/id_rsa")
		}
		if len(knownBytes) > 0 {
			execServiceIdxInput(dry, files, service, idx, knownBytes, "cat > '"+anchor+"'/.ssh/known_hosts && chmod 644 '"+anchor+"'/.ssh/known_hosts")
		}
		execServiceIdxInput(dry, files, service, idx, []byte(cfg), "cat > '"+anchor+"'/.ssh/config && chmod 600 '"+anchor+"'/.ssh/config")
		cmd := fmt.Sprintf(`set -e; home='%[1]s'; user_home="${HOME:-}"; if [ -z "$user_home" ] || [ ! -d "$user_home" ] || [ ! -w "$user_home" ]; then for candidate in /home/dev /home/node; do if [ -d "$candidate" ] && [ -w "$candidate" ]; then user_home="$candidate"; break; fi; done; fi; mkdir -p "$home" "$home/.ssh"; touch "$home/.gitconfig"; git config --file "$home/.gitconfig" core.sshCommand 'ssh -F %[1]s/.ssh/config'; if [ -n "$user_home" ] && [ "$user_home" != "$home" ]; then mkdir -p "$user_home"; if [ -e "$user_home/.ssh" ] && [ ! -L "$user_home/.ssh" ]; then rm -rf "$user_home/.ssh"; fi; ln -sfn "$home/.ssh" "$user_home/.ssh"; if [ -e "$user_home/.gitconfig" ] && [ ! -L "$user_home/.gitconfig" ]; then rm -f "$user_home/.gitconfig"; fi; ln -sfn "$home/.gitconfig" "$user_home/.gitconfig"; fi`, anchor)
		execServiceIdx(dry, files, service, idx, cmd)
	}
}

func prepareComposeProjectVolumes(dry bool, composeProject string) {
	if strings.TrimSpace(composeProject) == "" {
		return
	}
	ensureCoursierCacheVolume(dry, composeProject)
}

func ensureCoursierCacheVolume(dry bool, composeProject string) {
	volName := fmt.Sprintf("%s_coursier-cache", composeProject)
	if dry {
		fmt.Fprintf(os.Stderr, "+ ensure volume %s (dry-run)\n", volName)
		return
	}
	if !volumeExists(volName) {
		return
	}
	state, err := describeCoursierEntry(volName)
	if err != nil {
		return
	}
	if state == "file" || state == "other" {
		fmt.Fprintf(os.Stderr, "layout-apply: removing volume %s to repair coursier cache (unexpected %s at /cache/v1)\n", volName, state)
		runner.HostBestEffort(false, "docker", "volume", "rm", volName)
	}
}

func volumeExists(volumeName string) bool {
	ctx, cancel := execx.WithTimeout(5 * time.Second)
	defer cancel()
	_, res := execx.Capture(ctx, "docker", "volume", "inspect", volumeName, "--format", "{{.Name}}")
	return res.Code == 0
}

func describeCoursierEntry(volumeName string) (string, error) {
	ctx, cancel := execx.WithTimeout(15 * time.Second)
	defer cancel()
	script := "if [ -d /cache/v1 ]; then echo dir; elif [ -f /cache/v1 ]; then echo file; elif [ -e /cache/v1 ]; then echo other; else echo missing; fi"
	args := []string{"run", "--rm", "--pull", "missing", "-v", volumeName + ":/cache", "alpine:3.19", "sh", "-lc", script}
	out, res := execx.Capture(ctx, "docker", args...)
	if res.Code != 0 {
		if res.Err != nil {
			return "", res.Err
		}
		return "", fmt.Errorf("docker %s exited with code %d", strings.Join(args, " "), res.Code)
	}
	return strings.TrimSpace(out), nil
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
	tracker := agentexec.NewSeedTracker()
	cmdStr := mustBuildWindowCmd(fileArgs, project, idx, dest, service, tracker)
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
	tracker := agentexec.NewSeedTracker()
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
		cmdStr := mustBuildWindowCmd(fileArgs, project, idx, dest, service, tracker)
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
		cmdStr := mustBuildWindowCmd(fileArgs, project, idx, dest, service, tracker)
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
