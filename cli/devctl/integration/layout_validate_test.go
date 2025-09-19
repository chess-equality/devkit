//go:build integration
// +build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildDevctlBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "devctl")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./")
	build.Env = append(os.Environ(), "GO111MODULE=on")
	build.Dir = filepath.Join("..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func setupMinimalCompose(t *testing.T, root string) {
	t.Helper()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}
	base := "version: '3.8'\nservices:\n  dev-agent:\n    image: alpine:3\n    command: ['sh','-lc','sleep infinity']\n"
	write(filepath.Join(root, "kit/compose.yml"), base)
	write(filepath.Join(root, "kit/compose.dns.yml"), base)
	write(filepath.Join(root, "overlays/dev-all/compose.override.yml"), base)
}

func TestLayoutValidateSuccess(t *testing.T) {
	if os.Getenv("DEVKIT_INTEGRATION") != "1" {
		t.Skip("integration disabled")
	}
	bin := buildDevctlBinary(t)
	root := t.TempDir()
	setupMinimalCompose(t, root)

	layout := `session: devkit:ok
windows:
  - index: 1
    project: dev-all
    path: repo
  - index: 2
    project: dev-all
    path: repo
overlays:
  - project: dev-all
    count: 2
`
	ly := filepath.Join(root, "layout.yaml")
	if err := os.WriteFile(ly, []byte(layout), 0o644); err != nil {
		t.Fatalf("write layout: %v", err)
	}

	cmd := exec.Command(bin, "-p", "dev-all", "layout-validate", "--file", ly)
	cmd.Env = append(os.Environ(), "DEVKIT_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("layout-validate should succeed, err=%v, out=%s", err, out)
	}
}

func TestLayoutValidateDetectsErrors(t *testing.T) {
	if os.Getenv("DEVKIT_INTEGRATION") != "1" {
		t.Skip("integration disabled")
	}
	bin := buildDevctlBinary(t)
	root := t.TempDir()
	setupMinimalCompose(t, root)

	layout := `session: devkit:bad
windows:
  - index: 1
    project: dev-all
    path: repo
  - index: 2
    project: dev-all
    path: repo
overlays:
  - project: dev-all
    count: 1
`
	ly := filepath.Join(root, "layout.yaml")
	if err := os.WriteFile(ly, []byte(layout), 0o644); err != nil {
		t.Fatalf("write layout: %v", err)
	}

	var stderr bytes.Buffer
	cmd := exec.Command(bin, "-p", "dev-all", "layout-validate", "--file", ly)
	cmd.Env = append(os.Environ(), "DEVKIT_ROOT="+root)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("layout-validate expected failure, got success: %s", out)
	}
	combined := stderr.String() + string(out)
	if !strings.Contains(combined, "requires container index") {
		t.Fatalf("expected count error, output=%s", combined)
	}
}
