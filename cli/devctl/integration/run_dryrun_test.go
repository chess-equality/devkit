package main

import (
	"bytes"
	pth "devkit/cli/devctl/internal/paths"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun_DryRun ensures the run command produces expected docker compose invocations
// and does not error when invoked in dry-run mode with a minimal dev-all overlay.
func TestRun_DryRun(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := "version: '3.8'\nservices:\n  dev-agent:\n    image: alpine:3.18\n    command: ['sh','-lc','sleep 1']\n"
	write(filepath.Join(root, "kit/compose.yml"), base)
	write(filepath.Join(root, "kit/compose.dns.yml"), base)
	write(filepath.Join(root, "overlays/dev-all/compose.override.yml"), base)
	// files edited by allowlist step
	write(filepath.Join(root, "kit/proxy/allowlist.txt"), "\n")
	write(filepath.Join(root, "kit/dns/dnsmasq.conf"), "\n")

	// Build binary
	bin := filepath.Join(t.TempDir(), "devctl")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./")
	build.Env = append(os.Environ(), "GO111MODULE=on")
	build.Dir = filepath.Join("..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("go build failed: %v\n%s", err, out)
	}

	var stderr bytes.Buffer
	cmd := exec.Command(bin, "--dry-run", "--no-tmux", "--no-seed", "-p", "dev-all", "run", "testrepo", "2")
	cmd.Env = append(os.Environ(), "DEVKIT_ROOT="+root)
	cmd.Stderr = &stderr
	cmd.Stdout = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		t.Fatalf("run dry-run failed: %v\nstderr=%s", err, stderr.String())
	}
	out := stderr.String()
	// Expect compose up scaling and some execs
	wants := []string{
		"compose -f ",
		" up -d --remove-orphans --scale dev-agent=2",
		"docker exec -t devkit-devall-dev-agent-1",
		"/workspaces/dev/" + pth.AgentWorktreesDir + "/agent2/testrepo",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("missing %q in:\n%s", w, out)
		}
	}
}
