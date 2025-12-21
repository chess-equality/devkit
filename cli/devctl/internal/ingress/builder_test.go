package ingress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devkit/cli/devctl/internal/config"
)

func TestBuildFragmentFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overlays", "proj")
	if err := os.MkdirAll(filepath.Join(overlay, "infra"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(overlay, "infra", "Caddyfile")
	if err := os.WriteFile(cfgPath, []byte(":443 {\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.IngressConfig{
		Kind:   "caddy",
		Config: "infra/Caddyfile",
		Certs: []config.IngressCert{
			{Path: "/tmp/cert.pem"},
		},
	}
	frag, err := BuildFragment("proj", cfg, overlay, dir)
	if err != nil {
		t.Fatalf("BuildFragment error: %v", err)
	}
	if frag.Path == "" {
		t.Fatalf("missing fragment path")
	}
	data, err := os.ReadFile(frag.Path)
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	if !strings.Contains(string(data), cfgPath) {
		t.Fatalf("compose file did not include config path: %s", string(data))
	}
}

func TestBuildFragmentGeneratesConfigFromRoutes(t *testing.T) {
	dir := t.TempDir()
	project := "proj-routes"
	cfg := &config.IngressConfig{
		Kind: "caddy",
		Routes: []config.IngressRoute{
			{Host: "ouroboros.test", Service: "frontend", Port: 4173},
		},
	}
	frag, err := BuildFragment(project, cfg, "", dir)
	if err != nil {
		t.Fatalf("BuildFragment error: %v", err)
	}
	tmpDir := filepath.Join(os.TempDir(), "devkit-ingress", sanitize(project))
	genFile := filepath.Join(tmpDir, "Caddyfile.generated")
	if _, err := os.Stat(genFile); err != nil {
		t.Fatalf("expected generated config at %s: %v", genFile, err)
	}
	content, err := os.ReadFile(genFile)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(content), "ouroboros.test") {
		t.Fatalf("generated config missing host: %s", string(content))
	}
	if frag.Path == "" {
		t.Fatalf("missing fragment path")
	}
}
