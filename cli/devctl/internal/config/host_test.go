package config

import (
    "os"
    "path/filepath"
    "testing"
)

func TestReadHostConfig(t *testing.T) {
    dir := t.TempDir()
    cfgPath := filepath.Join(dir, "config.yaml")
    data := "overlay_paths:\n  - extras\ncli:\n  download_url: https://example.com/devctl\nenv:\n  FOO: bar\n"
    if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
        t.Fatal(err)
    }
    t.Setenv("DEVKIT_CONFIG", cfgPath)
    cfg, base, err := ReadHostConfig()
    if err != nil {
        t.Fatalf("ReadHostConfig error: %v", err)
    }
    if base != dir {
        t.Fatalf("expected base %q, got %q", dir, base)
    }
    if len(cfg.OverlayPaths) != 1 || cfg.OverlayPaths[0] != "extras" {
        t.Fatalf("overlay paths=%v", cfg.OverlayPaths)
    }
    if cfg.CLI.DownloadURL != "https://example.com/devctl" {
        t.Fatalf("download url=%q", cfg.CLI.DownloadURL)
    }
    if cfg.Env["FOO"] != "bar" {
        t.Fatalf("env map not loaded: %v", cfg.Env)
    }
}
