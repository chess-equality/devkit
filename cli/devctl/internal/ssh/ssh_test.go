package ssh

import "testing"

func TestBuildConfigureScripts(t *testing.T) {
	home := "/workspace/.devhome"
	repo := "/workspace"
	scripts := BuildConfigureScripts(home, repo)
	if len(scripts) != 5 {
		t.Fatalf("expected 5 scripts, got %d", len(scripts))
	}
	for i := 1; i < len(scripts); i++ {
		if scripts[i] == "" {
			t.Fatalf("script %d should not be empty: %#v", i, scripts)
		}
	}
	if scripts[2] == "" || scripts[3] == "" || scripts[4] == "" {
		t.Fatalf("scripts should not be empty: %#v", scripts)
	}
	// Ensure tilde-based config and repo path present
	if !contains(scripts[1], "$home/.ssh/config") && !contains(scripts[1], "/.ssh/config") {
		t.Fatalf("global set missing explicit config: %q", scripts[1])
	}
	if !contains(scripts[3], repo) {
		t.Fatalf("unset repo-level core.sshCommand missing repo: %q", scripts[3])
	}
	if !contains(scripts[4], "GIT_SSH_COMMAND=\"ssh -F ~/.ssh/config\"") && !contains(scripts[4], "GIT_SSH_COMMAND=\"ssh -F $home/.ssh/config\"") {
		t.Fatalf("pull cmd missing explicit GIT_SSH_COMMAND: %q", scripts[4])
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
