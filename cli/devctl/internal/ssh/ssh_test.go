package ssh

import "testing"

func TestBuildConfigureScripts(t *testing.T) {
	home := "/workspace/.devhome"
	repo := "/workspace"
	scripts := BuildConfigureScripts(home, repo)
	if len(scripts) != 4 {
		t.Fatalf("expected 4 scripts, got %d", len(scripts))
	}
	if scripts[1] == "" || scripts[2] == "" || scripts[3] == "" {
		t.Fatalf("scripts should not be empty: %#v", scripts)
	}
	// Ensure tilde-based config and repo path present
	if !contains(scripts[1], "$home/.ssh/config") && !contains(scripts[1], "/.ssh/config") {
		t.Fatalf("global set missing explicit config: %q", scripts[1])
	}
	if !contains(scripts[2], repo) {
		t.Fatalf("unset repo-level core.sshCommand missing repo: %q", scripts[2])
	}
	if !contains(scripts[3], "GIT_SSH_COMMAND=\"ssh -F ~/.ssh/config\"") && !contains(scripts[3], "GIT_SSH_COMMAND=\"ssh -F $home/.ssh/config\"") {
		t.Fatalf("pull cmd missing explicit GIT_SSH_COMMAND: %q", scripts[3])
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
