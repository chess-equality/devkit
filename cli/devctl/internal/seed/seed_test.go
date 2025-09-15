package seed

import "testing"

func TestBuildSeedScripts(t *testing.T) {
	scripts := BuildSeedScripts("/workspace/.devhome-agent1")
	if len(scripts) < 5 {
		t.Fatalf("expected >=5 scripts, got %d", len(scripts))
	}
	// Check presence of key steps
	mustContain := []string{
		"/var/host-codex", // wait condition
		"rm -rf '/workspace/.devhome-agent1/.codex'",
		"/var/host-codex/. '/workspace/.devhome-agent1/.codex/",
		"cp -f /var/auth.json '/workspace/.devhome-agent1/.codex/auth.json'",
		"chmod 600 '/workspace/.devhome-agent1/.codex/auth.json'",
	}
	joined := ""
	for _, s := range scripts {
		joined += s + "\n"
	}
	for _, m := range mustContain {
		if !contains(joined, m) {
			t.Fatalf("missing expected fragment: %q in scripts: %s", m, joined)
		}
	}
}

func TestBuildAnchorScripts(t *testing.T) {
	cfg := AnchorConfig{
		Anchor:    "/workspace/.devhome",
		Base:      "/workspace/.devhomes",
		SeedCodex: true,
	}
	scripts := BuildAnchorScripts(cfg)
	if len(scripts) < 2 {
		t.Fatalf("expected anchor script plus seeding, got %d", len(scripts))
	}
	first := scripts[0]
	for _, frag := range []string{"cid=$(hostname)", "/workspace/.devhomes"} {
		if !contains(first, frag) {
			t.Fatalf("anchor script missing %q: %s", frag, first)
		}
	}
	joined := JoinScripts(scripts)
	if !contains(joined, first) {
		t.Fatalf("joined scripts missing anchor: %s", joined)
	}
	if !contains(joined, "rm -rf '/workspace/.devhome/.codex'") {
		t.Fatalf("joined scripts missing codex reset: %s", joined)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (len(needle) == 0 || indexOf(hay, needle) >= 0)
}
func indexOf(h, n string) int {
	// simple substring search
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
