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
	if len(scripts) != 1 {
		t.Fatalf("expected single combined script, got %d", len(scripts))
	}
	sc := scripts[0]
	for _, frag := range []string{
		"target=\"/workspace/.devhomes/$(hostname)\"",
		"ln -sfn \"$target\" \"/workspace/.devhome\"",
		"rm -rf \"$target/.codex\"",
		"cp -a /var/host-codex/. \"$target/.codex/\"",
	} {
		if !contains(sc, frag) {
			t.Fatalf("combined script missing %q: %s", frag, sc)
		}
	}
	joined := JoinScripts(scripts)
	if !contains(joined, sc) {
		t.Fatalf("joined scripts missing combined anchor script: %s", joined)
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
