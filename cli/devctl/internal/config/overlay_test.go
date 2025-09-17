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
	got, err := ReadHooks(filepath.Join(dir, "overlays"), "proj")
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
