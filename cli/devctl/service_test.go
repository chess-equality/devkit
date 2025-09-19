package main

import (
	"os"
	"path/filepath"
	"testing"

	"devkit/cli/devctl/internal/config"
)

// TestResolveServiceOverlay ensures overlays can declare a default service name.
func TestResolveServiceOverlay(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// repo root: this file lives under devkit/cli/devctl
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	overlays := filepath.Join(root, "overlays")

	paths := []string{overlays}
	if got := resolveService("ouroboros-static-front-end", paths); got != "frontend" {
		t.Fatalf("resolveService(front-end) = %q, want %q", got, "frontend")
	}
	if got := resolveService("codex", paths); got != "dev-agent" {
		t.Fatalf("resolveService(codex) = %q, want %q", got, "dev-agent")
	}
}

func TestApplyOverlayEnvLoadsEnvFiles(t *testing.T) {
	dir := t.TempDir()
	overlayDir := filepath.Join(dir, "overlays", "proj")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(overlayDir, "extra.env")
	if err := os.WriteFile(envPath, []byte("FOO=123\nBAR=456\n# comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FOO", "keep")
	t.Setenv("BAZ", "existing")
	t.Setenv("WORKSPACE_DIR", "")
	cfg := config.OverlayConfig{
		Workspace: "repo",
		Env:       map[string]string{"BAZ": "updated", "QUX": "789"},
		EnvFiles:  []string{"extra.env"},
	}
	applyOverlayEnv(cfg, overlayDir, dir)
	if got := os.Getenv("WORKSPACE_DIR"); got != filepath.Join(overlayDir, "repo") {
		t.Fatalf("workspace_dir=%q", got)
	}
	if got := os.Getenv("FOO"); got != "keep" {
		t.Fatalf("env file should not override existing FOO, got %q", got)
	}
	if got := os.Getenv("BAR"); got != "456" {
		t.Fatalf("BAR not loaded from env file: %q", got)
	}
	if got := os.Getenv("QUX"); got != "789" {
		t.Fatalf("overlay env map not applied: %q", got)
	}
	if got := os.Getenv("BAZ"); got != "existing" {
		t.Fatalf("env map should not override existing BAZ, got %q", got)
	}
}
