package worktrees

import (
	"devkit/cli/devctl/internal/paths"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func readTrim(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func makeRepoWithBare(t *testing.T, root, devRoot, name string) {
	bare := filepath.Join(root, "remotes", name+".git")
	work := filepath.Join(devRoot, name)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "--bare", bare)
	mustRun(t, "git", "-C", work, "init")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", work, "add", ".")
	mustRun(t, "git", "-C", work, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-m", "init")
	mustRun(t, "git", "-C", work, "branch", "-M", "main")
	mustRun(t, "git", "-C", work, "remote", "add", "origin", bare)
	mustRun(t, "git", "-C", work, "push", "-u", "origin", "main")
}

func checkBranchAndUpstream(t *testing.T, path, wantBranch string) {
	t.Helper()
	got := readTrim(t, "git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD")
	if got != wantBranch {
		t.Fatalf("%s: want branch %s, got %s", path, wantBranch, got)
	}
	up := readTrim(t, "git", "-C", path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if up != "origin/main" {
		t.Fatalf("%s: want upstream origin/main, got %s", path, up)
	}
}

// This test performs a real host-side worktree setup across two repos with two agents each
// (agent1 in-place, agent2 as worktree), verifying branch names and upstreams.
func TestSetup_TwoRepos_TwoAgents(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Host layout: <root>/dev/{devkit, dumb-onion-hax, ouroboros-ide}, plus bare remotes under <root>/remotes
	root := t.TempDir()
	devRoot := filepath.Join(root, "dev")
	if err := os.MkdirAll(filepath.Join(devRoot, "devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	devkitRoot := filepath.Join(devRoot, "devkit")

	makeRepoWithBare(t, root, devRoot, "dumb-onion-hax")
	makeRepoWithBare(t, root, devRoot, "ouroboros-ide")

	// Run setup for each repo with two agents
	if err := Setup(devkitRoot, "dumb-onion-hax", 2, "main", "agent", false); err != nil {
		t.Fatalf("setup doh failed: %v", err)
	}
	if err := Setup(devkitRoot, "ouroboros-ide", 2, "main", "agent", false); err != nil {
		t.Fatalf("setup ouro failed: %v", err)
	}

	// Validate branches and upstreams
	checkBranchAndUpstream(t, filepath.Join(devRoot, "dumb-onion-hax"), "agent1")
	checkBranchAndUpstream(t, filepath.Join(devRoot, paths.AgentWorktreesDir, "agent2", "dumb-onion-hax"), "agent2")
	checkBranchAndUpstream(t, filepath.Join(devRoot, "ouroboros-ide"), "agent1")
	checkBranchAndUpstream(t, filepath.Join(devRoot, paths.AgentWorktreesDir, "agent2", "ouroboros-ide"), "agent2")
}

func TestSetup_RemovesStaleWorktreeDirectories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	devRoot := filepath.Join(root, "dev")
	if err := os.MkdirAll(filepath.Join(devRoot, "devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	devkitRoot := filepath.Join(devRoot, "devkit")

	makeRepoWithBare(t, root, devRoot, "dumb-onion-hax")
	makeRepoWithBare(t, root, devRoot, "ouroboros-ide")

	if err := Setup(devkitRoot, "dumb-onion-hax", 2, "main", "agent", false); err != nil {
		t.Fatalf("setup doh failed: %v", err)
	}
	if err := Setup(devkitRoot, "ouroboros-ide", 2, "main", "agent", false); err != nil {
		t.Fatalf("initial ouro setup failed: %v", err)
	}

	staleWt := filepath.Join(devRoot, paths.AgentWorktreesDir, "agent2", "ouroboros-ide")
	foreignGitdir := filepath.Join(devRoot, "dumb-onion-hax", ".git", "worktrees", "agent2")
	if err := os.WriteFile(filepath.Join(staleWt, ".git"), []byte("gitdir: "+foreignGitdir+"\n"), 0o644); err != nil {
		t.Fatalf("prepare stale gitdir: %v", err)
	}

	if err := Setup(devkitRoot, "ouroboros-ide", 2, "main", "agent", false); err != nil {
		t.Fatalf("ouro setup with stale dir failed: %v", err)
	}

	checkBranchAndUpstream(t, filepath.Join(devRoot, paths.AgentWorktreesDir, "agent2", "ouroboros-ide"), "agent2")
}
