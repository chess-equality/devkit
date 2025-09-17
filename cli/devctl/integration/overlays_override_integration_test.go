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
	"time"
)

// TestOverlaysOverride_Integration ensures DEVKIT_OVERLAYS_DIR can point to an
// overlay directory outside the kit root and that docker compose picks up the
// override when bringing up containers.
func TestOverlaysOverride_Integration(t *testing.T) {
	if os.Getenv("DEVKIT_INTEGRATION") != "1" {
		t.Skip("integration disabled; set DEVKIT_INTEGRATION=1 to run")
	}
	image := os.Getenv("DEVKIT_IT_IMAGE")
	if strings.TrimSpace(image) == "" {
		t.Skip("DEVKIT_IT_IMAGE not set; skipping")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	kitRoot := t.TempDir()
	overlaysRoot := filepath.Join(t.TempDir(), "overlays-root")
	if err := os.MkdirAll(filepath.Join(overlaysRoot, "test"), 0o755); err != nil {
		t.Fatalf("create overlay root: %v", err)
	}

	mustWrite := func(path, contents string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	base := "version: '3.8'\nservices:\n  dev-agent:\n    image: " + image + "\n    command: ['sh','-lc','sleep infinity']\n"
	mustWrite(filepath.Join(kitRoot, "kit/compose.yml"), base)
	mustWrite(filepath.Join(kitRoot, "kit/compose.dns.yml"), base)
	mustWrite(filepath.Join(kitRoot, "kit/compose.envoy.yml"), base)
	mustWrite(filepath.Join(kitRoot, "kit/proxy/allowlist.txt"), "\n")
	mustWrite(filepath.Join(kitRoot, "kit/dns/dnsmasq.conf"), "\n")

	labelKey := "devkit.test"
	labelValue := "overlay-override"
	override := "version: '3.8'\nservices:\n  dev-agent:\n    labels:\n      - " + labelKey + "=" + labelValue + "\n"
	mustWrite(filepath.Join(overlaysRoot, "test/compose.override.yml"), override)
	mustWrite(filepath.Join(overlaysRoot, "test/devkit.yaml"), "service: dev-agent\n")

	bin := filepath.Join(t.TempDir(), "devctl")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./")
	build.Env = append(os.Environ(), "GO111MODULE=on")
	build.Dir = filepath.Join("..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	// Bring up the overlay using the override directory.
	upCmd := exec.Command(bin, "-p", "test", "--no-tmux", "up")
	upCmd.Env = append(os.Environ(),
		"DEVKIT_ROOT="+kitRoot,
		"DEVKIT_OVERLAYS_DIR="+overlaysRoot,
		"DEVKIT_GIT_USER_NAME=integration",
		"DEVKIT_GIT_USER_EMAIL=integration@example.com",
	)
	var upErr bytes.Buffer
	upCmd.Stderr = &upErr
	if out, err := upCmd.Output(); err != nil {
		t.Fatalf("devctl up failed: %v\n%s\n%s", err, out, upErr.String())
	}
	defer func() {
		down := exec.Command(bin, "-p", "test", "--no-tmux", "down")
		down.Env = append(os.Environ(),
			"DEVKIT_ROOT="+kitRoot,
			"DEVKIT_OVERLAYS_DIR="+overlaysRoot,
		)
		_ = down.Run()
	}()

	// Wait briefly for the container to appear with the overlay label.
	found := false
	for i := 0; i < 5; i++ {
		ps := exec.Command("docker", "ps", "--filter", "label="+labelKey+"="+labelValue, "--format", "{{.Names}}")
		out, err := ps.CombinedOutput()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		if strings.TrimSpace(string(out)) != "" {
			found = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !found {
		t.Fatalf("container with label %s=%s not found", labelKey, labelValue)
	}
}
