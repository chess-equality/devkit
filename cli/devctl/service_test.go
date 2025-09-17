package main

import (
	"os"
	"path/filepath"
	"testing"
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

	if got := resolveService("ouroboros-static-front-end", overlays); got != "frontend" {
		t.Fatalf("resolveService(front-end) = %q, want %q", got, "frontend")
	}
	if got := resolveService("codex", overlays); got != "dev-agent" {
		t.Fatalf("resolveService(codex) = %q, want %q", got, "dev-agent")
	}
}
