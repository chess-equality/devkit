//go:build integration
// +build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutApply_MultiOverlayIsolation(t *testing.T) {
	if os.Getenv("DEVKIT_INTEGRATION") != "1" {
		t.Skip("integration disabled; set DEVKIT_INTEGRATION=1 to run")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	image := os.Getenv("DEVKIT_IT_IMAGE")
	if strings.TrimSpace(image) == "" {
		image = "alpine:3.18"
	}

	root := t.TempDir()
	mustWrite := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	base := "version: '3.8'\nservices:\n  tinyproxy:\n    image: alpine:3.18\n    command: ['sh','-c','sleep 3600']\n  dev-agent:\n    image: " + image + "\n    command: ['sh','-c','sleep 3600']\n    depends_on:\n      - tinyproxy\n"
	composeFiles := []string{"compose.yml", "compose.dns.yml", "compose.hardened.yml", "compose.envoy.yml"}
	for _, name := range composeFiles {
		mustWrite(filepath.Join(root, "kit", name), base)
	}

	overlays := []string{"alpha", "beta"}
	for _, ov := range overlays {
		dir := filepath.Join(root, "overlays", ov)
		mustWrite(filepath.Join(dir, "compose.override.yml"), "version: '3.8'\nservices:\n  dev-agent:\n    environment:\n      - OVERLAY="+ov+"\n")
		mustWrite(filepath.Join(dir, "devkit.yaml"), "service: dev-agent\n")
	}

	layout := "session: multi\noverlays:\n  - project: alpha\n    service: dev-agent\n    count: 1\n    profiles: dns\n    compose_project: multi-alpha\n  - project: beta\n    service: dev-agent\n    count: 1\n    profiles: dns\n    compose_project: multi-beta\nwindows: []\n"
	layoutPath := filepath.Join(root, "layout.yaml")
	mustWrite(layoutPath, layout)

	bin := filepath.Join(t.TempDir(), "devctl")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./")
	build.Env = append(os.Environ(), "GO111MODULE=on")
	build.Dir = filepath.Join("..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "layout-apply", "--file", layoutPath)
	cmd.Env = append(os.Environ(), "DEVKIT_ROOT="+root, "DEVKIT_NO_TMUX=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("layout-apply failed: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		bringDown := func(project, overlay string) {
			args := []string{"compose", "-p", project,
				"-f", filepath.Join(root, "kit", "compose.yml"),
				"-f", filepath.Join(root, "kit", "compose.dns.yml"),
				"-f", filepath.Join(root, "overlays", overlay, "compose.override.yml"),
				"down", "--remove-orphans"}
			_ = exec.Command("docker", args...).Run()
		}
		bringDown("multi-alpha", "alpha")
		bringDown("multi-beta", "beta")
	})

	expectService := func(project string) {
		args := []string{"ps", "--filter", "label=com.docker.compose.project=" + project,
			"--filter", "label=com.docker.compose.service=tinyproxy", "-q"}
		out, err := exec.Command("docker", args...).Output()
		if err != nil {
			t.Fatalf("docker ps failed: %v", err)
		}
		if strings.TrimSpace(string(out)) == "" {
			t.Fatalf("tinyproxy for %s not found", project)
		}
	}

	expectService("multi-alpha")
	expectService("multi-beta")
}
