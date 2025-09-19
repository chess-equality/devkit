package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadHooks_FromYaml(t *testing.T) {
	dir := t.TempDir()
	over := filepath.Join(dir, "overlays", "proj")
	if err := os.MkdirAll(over, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "" +
		"workspace: ../../x\n" +
		"env:\n  FOO: bar\n" +
		"hooks:\n  warm: echo 'hi'\n  maintain: echo world\n"
	if err := os.WriteFile(filepath.Join(over, "devkit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
    got, err := ReadHooks([]string{filepath.Join(dir, "overlays")}, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if got.Warm != "echo 'hi'" {
		t.Fatalf("warm=%q", got.Warm)
	}
	if got.Maintain != "echo world" {
		t.Fatalf("maintain=%q", got.Maintain)
	}
}

func TestReadAllIncludesWorkspaceAndEnv(t *testing.T) {
	dir := t.TempDir()
	over := filepath.Join(dir, "overlays", "proj")
	if err := os.MkdirAll(over, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "" +
		"workspace: ../../my-repo\n" +
		"env:\n  FOO: bar\n  BAZ: qux\n"
	if err := os.WriteFile(filepath.Join(over, "devkit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
    cfg, dirPath, err := ReadAll([]string{filepath.Join(dir, "overlays")}, "proj")
    if err != nil {
        t.Fatalf("ReadAll error: %v", err)
    }
    if dirPath != over {
        t.Fatalf("overlay dir=%q", dirPath)
    }
    if cfg.Workspace != "../../my-repo" {
        t.Fatalf("workspace=%q", cfg.Workspace)
    }
    if cfg.Env == nil || cfg.Env["FOO"] != "bar" || cfg.Env["BAZ"] != "qux" {
        t.Fatalf("env=%v", cfg.Env)
    }
}

func TestReadAllSkipsMissing(t *testing.T) {
    cfg, dirPath, err := ReadAll([]string{"/does/not/exist"}, "proj")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if dirPath != "" {
        t.Fatalf("expected empty dir, got %q", dirPath)
    }
    if cfg.Env != nil && len(cfg.Env) > 0 {
        t.Fatalf("expected empty env, got %v", cfg.Env)
    }
}
