package testutil

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	mathrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devkit/cli/devctl/internal/paths"
)

// CmdResult captures stdout, stderr, and the resulting error from a command execution.
type CmdResult struct {
	Stdout string
	Stderr string
	Err    error
}

// Success reports whether the underlying command exited without error.
func (r CmdResult) Success() bool {
	return r.Err == nil
}

type overlayRegistration struct {
	ComposeProject string
	Overlay        string
	Profiles       string
}

// RuntimeFixture prepares a temporary devkit root, repositories, and convenience helpers
// for exercising the runtime-config workflow end-to-end.
type RuntimeFixture struct {
	t            *testing.T
	repoRoot     string
	root         string
	devRoot      string
	devkitRoot   string
	worktreeRoot string
	workspaceDir string
	remotesDir   string

	imageContext string

	envBase map[string]string

	registered []overlayRegistration
}

// NewRuntimeFixture constructs a ready-to-use runtime integration harness. Tests should ensure
// docker and git are available before invoking this helper.
func NewRuntimeFixture(t *testing.T) *RuntimeFixture {
	t.Helper()

	requireBinary(t, "docker")
	requireBinary(t, "git")

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("detect repo root: %v", err)
	}

	root := t.TempDir()
	devRoot := filepath.Join(root, "dev")
	if err := os.MkdirAll(devRoot, 0o755); err != nil {
		t.Fatalf("mkdir dev root: %v", err)
	}
	devkitRoot := filepath.Join(devRoot, "devkit")
	if err := os.MkdirAll(devkitRoot, 0o755); err != nil {
		t.Fatalf("mkdir devkit root: %v", err)
	}

	workspaceDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	remotesDir := filepath.Join(root, "remotes")
	if err := os.MkdirAll(remotesDir, 0o755); err != nil {
		t.Fatalf("mkdir remotes: %v", err)
	}

	worktreeRoot := filepath.Join(devRoot, paths.AgentWorktreesDir)
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatalf("mkdir worktree root: %v", err)
	}

	copyTestAssets(t, repoRoot, devkitRoot)

	imageContext := filepath.Join(repoRoot, "testing", "runtime")
	envBase := mapFromEnviron(os.Environ())
	envBase["DEVKIT_ROOT"] = devkitRoot
	envBase["DEVKIT_ENABLE_RUNTIME_CONFIG"] = "1"
	envBase["DEVKIT_WORKTREE_ROOT"] = worktreeRoot
	envBase["DEVKIT_RUNTIME_WORKSPACE"] = workspaceDir
	envBase["DEVKIT_RUNTIME_DEVROOT"] = devRoot
	envBase["DEVKIT_RUNTIME_REMOTES"] = remotesDir
	envBase["DEVKIT_RUNTIME_ROOT"] = root
	envBase["DEVKIT_RUNTIME_IMAGE_CONTEXT"] = imageContext
	envBase["DEVKIT_RUNTIME_IMAGE"] = "devkit/runtime-integration:latest"
	envBase["DEVKIT_RUNTIME_UID"] = fmt.Sprint(os.Getuid())
	envBase["DEVKIT_RUNTIME_GID"] = fmt.Sprint(os.Getgid())
	envBase["DEVKIT_NO_TMUX"] = "1"
	envBase["DEVKIT_GIT_USER_NAME"] = "runtime-tests"
	envBase["DEVKIT_GIT_USER_EMAIL"] = "runtime-tests@example.com"
	envBase["GIT_CONFIG_GLOBAL"] = "/dev/null"
	envBase["DEVKIT_SKIP_SHARED_CLEANUP"] = "1"

	stubBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(stubBin, 0o755); err != nil {
		t.Fatalf("mkdir stub bin: %v", err)
	}
	tmuxStub := filepath.Join(stubBin, "tmux")
	stubScript := "#!/bin/sh\n# tmux stub for runtime tests\nexit 0\n"
	if err := os.WriteFile(tmuxStub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write tmux stub: %v", err)
	}
	if pathVal, ok := envBase["PATH"]; ok && pathVal != "" {
		envBase["PATH"] = stubBin + string(os.PathListSeparator) + pathVal
	} else {
		envBase["PATH"] = stubBin
	}

	f := &RuntimeFixture{
		t:            t,
		repoRoot:     repoRoot,
		root:         root,
		devRoot:      devRoot,
		devkitRoot:   devkitRoot,
		worktreeRoot: worktreeRoot,
		workspaceDir: workspaceDir,
		remotesDir:   remotesDir,
		imageContext: imageContext,
		envBase:      envBase,
	}

	build := exec.Command("make", "-C", filepath.Join(repoRoot, "cli/devctl"), "build")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devctl binary: %v\n%s", err, out)
	}

	f.initRepo("ouroboros-ide")
	f.initRepo("dumb-onion-hax")
	// Auxiliary repo for front overlay expectations.
	if err := os.MkdirAll(filepath.Join(f.devRoot, "front-repo"), 0o755); err != nil {
		t.Fatalf("mkdir front-repo: %v", err)
	}

	t.Cleanup(f.cleanup)
	return f
}

// DevkitRoot returns the temporary DEVKIT_ROOT value.
func (f *RuntimeFixture) DevkitRoot() string { return f.devkitRoot }

// WorktreeRoot returns the host path backing DEVKIT_WORKTREE_ROOT.
func (f *RuntimeFixture) WorktreeRoot() string { return f.worktreeRoot }

// WorkspaceDir returns the host path bound to /workspace for codex overlay checks.
func (f *RuntimeFixture) WorkspaceDir() string { return f.workspaceDir }

// DevRoot returns the parent dev root containing repositories.
func (f *RuntimeFixture) DevRoot() string { return f.devRoot }

// RemotesDir returns the location of the bare git remotes used by host+container git pulls.
func (f *RuntimeFixture) RemotesDir() string { return f.remotesDir }

// ComposeProject computes a unique docker compose project name suffixing the overlay.
func (f *RuntimeFixture) ComposeProject(overlay string) string {
	base := "devkit-test-" + randomSuffix(6)
	return base + "-" + sanitize(overlay)
}

// TrackOverlay ensures docker compose down is invoked for the provided overlay/project during cleanup.
func (f *RuntimeFixture) TrackOverlay(project string, overlay string, profiles string) {
	f.registered = append(f.registered, overlayRegistration{
		ComposeProject: project,
		Overlay:        overlay,
		Profiles:       profiles,
	})
}

// WriteFile writes relative to the devkit root and returns the absolute path.
func (f *RuntimeFixture) WriteFile(rel string, content string) string {
	path := filepath.Join(f.devkitRoot, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

// Environ returns the base environment merged with overrides for command execution.
func (f *RuntimeFixture) Environ(overrides map[string]string) []string {
	env := make(map[string]string, len(f.envBase)+len(overrides))
	for k, v := range f.envBase {
		env[k] = v
	}
	for k, v := range overrides {
		if v == "" {
			delete(env, k)
			continue
		}
		env[k] = v
	}
	return mapToEnviron(env)
}

// RunDevkit executes kit/scripts/devkit with the provided arguments and environment overrides.
func (f *RuntimeFixture) RunDevkit(overrides map[string]string, args ...string) CmdResult {
	return f.RunDevkitContext(context.Background(), overrides, args...)
}

// RunDevkitContext executes kit/scripts/devkit with a context for timeout control.
func (f *RuntimeFixture) RunDevkitContext(ctx context.Context, overrides map[string]string, args ...string) CmdResult {
	if ctx == nil {
		ctx = context.Background()
	}
	path := filepath.Join(f.repoRoot, "kit", "scripts", "devkit")
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = f.Environ(overrides)
	cmd.Dir = f.repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CmdResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

func (f *RuntimeFixture) cleanup() {
	for i := len(f.registered) - 1; i >= 0; i-- {
		info := f.registered[i]
		files := f.composeFileArgs(info.Overlay, info.Profiles)
		args := append([]string{"compose", "-p", info.ComposeProject}, append(files, "down", "--remove-orphans")...)
		cmd := exec.Command("docker", args...)
		cmd.Env = f.Environ(nil)
		cmd.Dir = f.repoRoot
		_ = cmd.Run() // best effort
	}
}

func (f *RuntimeFixture) composeFileArgs(overlay string, profiles string) []string {
	files := []string{
		"-f", filepath.Join(f.devkitRoot, "kit", "compose.yml"),
		"-f", filepath.Join(f.devkitRoot, "kit", "compose.dns.yml"),
	}
	if strings.Contains(profiles, "hardened") {
		files = append(files, "-f", filepath.Join(f.devkitRoot, "kit", "compose.hardened.yml"))
	}
	if strings.Contains(profiles, "envoy") {
		files = append(files, "-f", filepath.Join(f.devkitRoot, "kit", "compose.envoy.yml"))
	}
	if strings.Contains(profiles, "pool") {
		files = append(files, "-f", filepath.Join(f.devkitRoot, "kit", "compose.pool.yml"))
	}
	files = append(files, "-f", filepath.Join(f.devkitRoot, "overlays", overlay, "compose.override.yml"))
	return files
}

func (f *RuntimeFixture) initRepo(name string) {
	bare := filepath.Join(f.remotesDir, name+".git")
	work := filepath.Join(f.devRoot, name)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		f.t.Fatalf("mkdir bare %s: %v", name, err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		f.t.Fatalf("mkdir work %s: %v", name, err)
	}
	runGit(f.t, "git", "init", "--bare", bare)
	runGit(f.t, "git", "-C", work, "init")
	readme := filepath.Join(work, "README.md")
	if err := os.WriteFile(readme, []byte("# "+name+"\n"), 0o644); err != nil {
		f.t.Fatalf("write README %s: %v", name, err)
	}
	runGit(f.t, "git", "-C", work, "add", ".")
	runGit(f.t, "git", "-C", work, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-m", "init")
	runGit(f.t, "git", "-C", work, "branch", "-M", "main")
	runGit(f.t, "git", "-C", work, "remote", "add", "origin", bare)
	runGit(f.t, "git", "-C", work, "push", "-u", "origin", "main")
}

func copyTestAssets(t *testing.T, repoRoot, devkitRoot string) {
	t.Helper()
	srcKit := filepath.Join(repoRoot, "testing", "runtime", "kit")
	dstKit := filepath.Join(devkitRoot, "kit")
	if err := copyDir(srcKit, dstKit); err != nil {
		t.Fatalf("copy kit assets: %v", err)
	}
	srcOverlays := filepath.Join(repoRoot, "testing", "runtime", "overlays")
	dstOverlays := filepath.Join(devkitRoot, "overlays")
	if err := copyDir(srcOverlays, dstOverlays); err != nil {
		t.Fatalf("copy overlay assets: %v", err)
	}
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		sPath := filepath.Join(src, entry.Name())
		dPath := filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := copyDir(sPath, dPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(sPath, dPath, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, perm)
}

func runGit(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func requireBinary(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not available: %v", name, err)
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "kit", "scripts", "devkit")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("repository root not found")
}

func randomSuffix(n int) string {
	buff := make([]byte, int(math.Ceil(float64(n)/2)))
	if _, err := rand.Read(buff); err != nil {
		mathrand.Seed(time.Now().UnixNano())
		return fmt.Sprintf("%0*x", n, mathrand.Int63())
	}
	return hex.EncodeToString(buff)[:n]
}

func sanitize(s string) string {
	if s == "" {
		return "overlay"
	}
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, s)
	return strings.Trim(out, "-")
}

func mapFromEnviron(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if kv == "" {
			continue
		}
		if idx := strings.IndexRune(kv, '='); idx >= 0 {
			m[kv[:idx]] = kv[idx+1:]
		}
	}
	return m
}

func mapToEnviron(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
