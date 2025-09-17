package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectPathsFromExe_OverlayOverride(t *testing.T) {
	t.Setenv("DEVKIT_ROOT", "/tmp/devkit")
	t.Setenv("DEVKIT_OVERLAYS_DIR", "/tmp/project-overlays")
	p, err := DetectPathsFromExe("/tmp/devkit/kit/bin/devctl")
	if err != nil {
		t.Fatalf("DetectPathsFromExe failed: %v", err)
	}
	if p.Root != "/tmp/devkit" {
		t.Fatalf("unexpected root: %s", p.Root)
	}
	if p.Kit != "/tmp/devkit/kit" {
		t.Fatalf("unexpected kit: %s", p.Kit)
	}
	if p.Overlays != "/tmp/project-overlays" {
		t.Fatalf("unexpected overlays: %s", p.Overlays)
	}
}

func TestDetectPathsFromExe_OverlayRelativeOverride(t *testing.T) {
	t.Setenv("DEVKIT_ROOT", "/tmp/devkit")
	t.Setenv("DEVKIT_OVERLAYS_DIR", "../custom-overlays")
	p, err := DetectPathsFromExe("/tmp/devkit/kit/bin/devctl")
	if err != nil {
		t.Fatalf("DetectPathsFromExe failed: %v", err)
	}
	expected := filepath.Clean("/tmp/custom-overlays")
	if p.Overlays != expected {
		t.Fatalf("expected overlays %s, got %s", expected, p.Overlays)
	}
}

func TestFiles_DefaultDns(t *testing.T) {
	p := Paths{Root: "/repo/devkit", Kit: "/repo/devkit/kit", Overlays: "/repo/devkit/overlays"}
	got, err := Files(p, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := filepath.Join(p.Kit, "compose.yml")
	if len(got) < 2 || got[0] != "-f" || got[1] != wantFirst {
		t.Fatalf("unexpected base files: %v", got)
	}
}

func TestFiles_ProfilesAndOverlay(t *testing.T) {
	dir := t.TempDir()
	kit := filepath.Join(dir, "kit")
	overlays := filepath.Join(dir, "overlays", "proj")
	os.MkdirAll(kit, 0o755)
	os.MkdirAll(overlays, 0o755)
	// create compose files
	for _, f := range []string{"compose.yml", "compose.hardened.yml", "compose.dns.yml", "compose.envoy.yml"} {
		if err := os.WriteFile(filepath.Join(kit, f), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// overlay compose override
	if err := os.WriteFile(filepath.Join(overlays, "compose.override.yml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Paths{Root: dir, Kit: kit, Overlays: filepath.Join(dir, "overlays")}
	got, err := Files(p, "proj", "hardened,dns,envoy")
	if err != nil {
		t.Fatal(err)
	}
	// expect 10 elements: -f base, -f hardened, -f dns, -f envoy, -f overlay
	if len(got) != 10 {
		t.Fatalf("want 10 args, got %d: %v", len(got), got)
	}
	if got[1] != filepath.Join(kit, "compose.yml") {
		t.Fatalf("base wrong: %v", got)
	}
	if got[3] != filepath.Join(kit, "compose.hardened.yml") {
		t.Fatalf("hardened missing: %v", got)
	}
	if got[5] != filepath.Join(kit, "compose.dns.yml") {
		t.Fatalf("dns missing: %v", got)
	}
	if got[7] != filepath.Join(kit, "compose.envoy.yml") {
		t.Fatalf("envoy missing: %v", got)
	}
	if got[9] != filepath.Join(overlays, "compose.override.yml") {
		t.Fatalf("overlay missing: %v", got)
	}
}

func TestFiles_UnknownProfile(t *testing.T) {
	p := Paths{Root: "/r", Kit: "/r/kit", Overlays: "/r/overlays"}
	if _, err := Files(p, "proj", "invalid"); err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

func TestAllProfilesFiles(t *testing.T) {
	dir := t.TempDir()
	kit := filepath.Join(dir, "kit")
	overlays := filepath.Join(dir, "overlays", "proj")
	os.MkdirAll(kit, 0o755)
	os.MkdirAll(overlays, 0o755)
	for _, f := range []string{"compose.yml", "compose.hardened.yml", "compose.dns.yml", "compose.envoy.yml"} {
		if err := os.WriteFile(filepath.Join(kit, f), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(overlays, "compose.override.yml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Paths{Root: dir, Kit: kit, Overlays: filepath.Join(dir, "overlays")}
	got := AllProfilesFiles(p, "proj")
	if len(got) != 10 {
		t.Fatalf("want 10 args, got %d: %v", len(got), got)
	}
}
