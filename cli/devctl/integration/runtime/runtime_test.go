package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devkit/cli/devctl/internal/paths"
	"devkit/cli/devctl/internal/testutil"
)

func requireRuntimeFixture(t *testing.T) *testutil.RuntimeFixture {
	t.Helper()
	if os.Getenv("DEVKIT_RUNTIME_TESTS") != "1" {
		t.Skip("set DEVKIT_RUNTIME_TESTS=1 to run runtime integration tests")
	}
	return testutil.NewRuntimeFixture(t)
}

func TestDoctorRuntime(t *testing.T) {
	fx := requireRuntimeFixture(t)

	project := fx.ComposeProject("codex")
	fx.TrackOverlay(project, "codex", "dns")

	// Bring up the codex overlay with a unique compose project.
	up := fx.RunDevkit(map[string]string{
		"COMPOSE_PROJECT_NAME": project,
	}, "-p", "codex", "up")
	if up.Err != nil {
		t.Fatalf("compose up failed: %v\nstdout:\n%s\nstderr:\n%s", up.Err, up.Stdout, up.Stderr)
	}

	time.Sleep(2 * time.Second) // give the container a moment to settle

	res := fx.RunDevkit(map[string]string{
		"COMPOSE_PROJECT_NAME": project,
	}, "-p", "codex", "doctor-runtime")
	if res.Err != nil {
		t.Fatalf("doctor-runtime failed: %v\nstdout:\n%s\nstderr:\n%s", res.Err, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "mounts /worktrees") {
		t.Fatalf("doctor-runtime output missing mount confirmation:\n%s", res.Stdout)
	}
}

func TestLayoutApplyRuntimeWorktrees(t *testing.T) {
	fx := requireRuntimeFixture(t)

	devAllProject := fx.ComposeProject("devall")
	frontProject := fx.ComposeProject("front")
	t.Setenv("DEVKIT_COMPOSE_PROJECT_DEV_ALL", devAllProject)
	t.Setenv("DEVKIT_COMPOSE_PROJECT_FRONT", frontProject)
	fx.TrackOverlay(devAllProject, "dev-all", "dns")
	fx.TrackOverlay(frontProject, "front", "dns")

	layout := `session: runtime:test

overlays:
  - project: dev-all
    service: dev-agent
    count: 2
    profiles: dns
    compose_project: ` + devAllProject + `
    worktrees:
      repo: ouroboros-ide
      count: 2
      base_branch: main
      branch_prefix: agent
  - project: front
    service: dev-agent
    count: 1
    profiles: dns
    compose_project: ` + frontProject + `

windows:
  - index: 1
    project: dev-all
    service: dev-agent
    path: ouroboros-ide
    name: dev1
  - index: 2
    project: dev-all
    service: dev-agent
    path: agent-worktrees/agent2/ouroboros-ide
    name: dev2
`
	layoutPath := fx.WriteFile("layouts/runtime.yaml", layout)

	// Dry-run should succeed without spinning up containers.
	dry := fx.RunDevkit(nil, "-p", "dev-all", "--dry-run", "layout-apply", "--file", layoutPath)
	if dry.Err != nil {
		t.Fatalf("layout-apply dry-run failed: %v\nstdout:\n%s\nstderr:\n%s", dry.Err, dry.Stdout, dry.Stderr)
	}

	apply := fx.RunDevkit(nil, "-p", "dev-all", "layout-apply", "--file", layoutPath)
	if apply.Err != nil {
		t.Fatalf("layout-apply failed: %v\nstdout:\n%s\nstderr:\n%s", apply.Err, apply.Stdout, apply.Stderr)
	}

	// Host worktrees exist and track expected branches.
	agent1 := filepath.Join(fx.DevRoot(), "ouroboros-ide")
	agent2 := filepath.Join(fx.DevRoot(), paths.AgentWorktreesDir, "agent2", "ouroboros-ide")
	if branch := gitTrim(t, agent1, "rev-parse", "--abbrev-ref", "HEAD"); branch != "agent1" {
		t.Fatalf("agent1 branch mismatch: %s", branch)
	}
	if branch := gitTrim(t, agent2, "rev-parse", "--abbrev-ref", "HEAD"); branch != "agent2" {
		t.Fatalf("agent2 branch mismatch: %s", branch)
	}

	// Ensure git status succeeds inside agent 1.
	status := fx.RunDevkit(map[string]string{
		"COMPOSE_PROJECT_NAME": devAllProject,
	}, "-p", "dev-all", "worktrees-status", "ouroboros-ide", "--index", "1")
	if status.Err != nil {
		t.Fatalf("worktrees-status failed: %v\nstdout:\n%s\nstderr:\n%s", status.Err, status.Stdout, status.Stderr)
	}
	if !strings.Contains(status.Stdout, "## agent1") {
		t.Fatalf("worktrees-status output unexpected:\n%s", status.Stdout)
	}

	sync := fx.RunDevkit(map[string]string{
		"COMPOSE_PROJECT_NAME": devAllProject,
	}, "-p", "dev-all", "worktrees-sync", "ouroboros-ide", "--pull", "--index", "1")
	if sync.Err != nil {
		t.Fatalf("worktrees-sync failed: %v\nstdout:\n%s\nstderr:\n%s", sync.Err, sync.Stdout, sync.Stderr)
	}

	push := fx.RunDevkit(map[string]string{
		"COMPOSE_PROJECT_NAME": devAllProject,
	}, "-p", "dev-all", "worktrees-sync", "ouroboros-ide", "--push", "--index", "1")
	if push.Err != nil {
		t.Fatalf("worktrees-sync --push failed: %v\nstdout:\n%s\nstderr:\n%s", push.Err, push.Stdout, push.Stderr)
	}

	verifyCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	verify := fx.RunDevkitContext(verifyCtx, map[string]string{
		"COMPOSE_PROJECT_NAME": devAllProject,
	}, "-p", "dev-all", "verify")
	if verify.Err != nil {
		t.Fatalf("verify failed: %v\nstdout:\n%s\nstderr:\n%s", verify.Err, verify.Stdout, verify.Stderr)
	}
	if !strings.Contains(verify.Stdout, "verify completed") {
		t.Fatalf("verify output unexpected:\n%s", verify.Stdout)
	}
}

func TestVerifyAllRuntime(t *testing.T) {
	fx := requireRuntimeFixture(t)

	codexProject := fx.ComposeProject("codex")
	devAllProject := fx.ComposeProject("verify-devall")
	t.Setenv("DEVKIT_COMPOSE_PROJECT_CODEX", codexProject)
	t.Setenv("DEVKIT_COMPOSE_PROJECT_DEV_ALL", devAllProject)
	fx.TrackOverlay(codexProject, "codex", "dns")
	fx.TrackOverlay(devAllProject, "dev-all", "dns")

	upCodex := fx.RunDevkit(map[string]string{
		"COMPOSE_PROJECT_NAME": codexProject,
	}, "-p", "codex", "up")
	if upCodex.Err != nil {
		t.Fatalf("codex compose up failed: %v\nstdout:\n%s\nstderr:\n%s", upCodex.Err, upCodex.Stdout, upCodex.Stderr)
	}

	upDevAll := fx.RunDevkit(map[string]string{
		"COMPOSE_PROJECT_NAME": devAllProject,
	}, "-p", "dev-all", "up")
	if upDevAll.Err != nil {
		t.Fatalf("dev-all compose up failed: %v\nstdout:\n%s\nstderr:\n%s", upDevAll.Err, upDevAll.Stdout, upDevAll.Stderr)
	}

	res := fx.RunDevkit(nil, "verify-all")
	if res.Err != nil {
		t.Fatalf("verify-all failed: %v\nstdout:\n%s\nstderr:\n%s", res.Err, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "verify completed") {
		t.Fatalf("verify-all output missing success marker:\n%s", res.Stdout)
	}
}

func TestLayoutApplyMissingRepoFails(t *testing.T) {
	fx := requireRuntimeFixture(t)

	devAllProject := fx.ComposeProject("devall")
	t.Setenv("DEVKIT_COMPOSE_PROJECT_DEV_ALL", devAllProject)
	fx.TrackOverlay(devAllProject, "dev-all", "dns")

	layout := `session: runtime:missing

overlays:
  - project: dev-all
    service: dev-agent
    count: 1
    profiles: dns
    compose_project: ` + devAllProject + `
    worktrees:
      repo: missing-repo
      count: 1
`
	layoutPath := fx.WriteFile("layouts/missing.yaml", layout)

	res := fx.RunDevkit(nil, "-p", "dev-all", "layout-apply", "--file", layoutPath)
	if res.Err == nil {
		t.Fatalf("layout-apply succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "workspace directory") &&
		!strings.Contains(res.Stderr, "does not exist") &&
		!strings.Contains(res.Stderr, "No such file or directory") &&
		!strings.Contains(res.Stderr, "missing-repo") {
		t.Fatalf("layout-apply error did not mention missing repo:\n%s", res.Stderr)
	}
}

func gitTrim(t *testing.T, repoPath string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := execCommand("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (%s): %v\n%s", args, repoPath, err, out)
	}
	return strings.TrimSpace(string(out))
}

func execCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	return cmd
}
