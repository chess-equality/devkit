//go:build integration
// +build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Dry-run integration: ensure layout-apply parses a layout with dev-all worktrees and
// emits compose/tmux commands without executing them.
func TestLayoutApply_Worktrees_DryRun(t *testing.T) {
	if os.Getenv("DEVKIT_INTEGRATION") != "1" {
		t.Skip("integration disabled")
	}
	bin := filepath.Join(t.TempDir(), "devctl")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./")
	build.Env = append(os.Environ(), "GO111MODULE=on")
	build.Dir = filepath.Join("..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	root := t.TempDir()
	write := func(p, s string) { os.MkdirAll(filepath.Dir(p), 0o755); os.WriteFile(p, []byte(s), 0o644) }
	// Minimal compose files; dry-run prints commands only
	base := "version: '3.8'\nservices:\n  dev-agent:\n    image: alpine:3\n    command: ['sh','-lc','sleep infinity']\n"
	write(filepath.Join(root, "kit/compose.yml"), base)
	write(filepath.Join(root, "kit/compose.dns.yml"), base)
	write(filepath.Join(root, "overlays/dev-all/compose.override.yml"), base)

	// Layout with dev-all worktrees for two repos
	layout := `session: devkit:mixed
overlays:
  - project: dev-all
    service: dev-agent
    count: 2
    profiles: dns
    compose_project: devkit-devall
    worktrees:
      repo: dumb-onion-hax
      count: 2
      base_branch: main
      branch_prefix: agent
  - project: dev-all
    service: dev-agent
    count: 2
    profiles: dns
    compose_project: devkit-devall2
    worktrees:
      repo: ouroboros-ide
      count: 2
      base_branch: main
      branch_prefix: agent
windows:
  - index: 1
    project: dev-all
    service: dev-agent
    path: dumb-onion-hax
    name: doh-1
  - index: 2
    project: dev-all
    service: dev-agent
    path: agent-worktrees/agent2/dumb-onion-hax
    name: doh-2
`
	ly := filepath.Join(root, "orchestration.yaml")
	write(ly, layout)

	var stderr bytes.Buffer
	cmd := exec.Command(bin, "-p", "dev-all", "--dry-run", "layout-apply", "--file", ly)
	cmd.Env = append(os.Environ(), "DEVKIT_ROOT="+root, "DEVKIT_NO_TMUX=1")
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("layout-apply failed: %v\n%s\n%s", err, out, stderr.String())
	}
}
