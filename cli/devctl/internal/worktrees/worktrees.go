package worktrees

import (
	"context"
	"devkit/cli/devctl/internal/execx"
	"devkit/cli/devctl/internal/paths"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// run runs a host command with timeout and prints when DEVKIT_DEBUG=1.
func run(dry bool, name string, args ...string) error {
	if dry {
		fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(append([]string{name}, args...), " "))
		return nil
	}
	ctx, cancel := execx.WithTimeout(10_000_000_000) // 10s default; outer callers usually wrap
	defer cancel()
	res := execx.RunCtx(ctx, name, args...)
	if res.Code != 0 {
		return fmt.Errorf("%s %v: exit %d", name, args, res.Code)
	}
	return nil
}

// rewriteGitdir writes a .git file pointing to a relative gitdir for container correctness.
func rewriteGitdir(wt string) {
	out, res := execx.Capture(context.Background(), "git", "-C", wt, "rev-parse", "--git-dir")
	if res.Code != 0 {
		return
	}
	gitdir := strings.TrimSpace(out)
	if gitdir == "" {
		return
	}
	rel, err := filepath.Rel(wt, gitdir)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+rel+"\n"), 0644)
}

// Setup ensures worktrees and branches exist for agents 1..n.
// devkitRoot: path to devkit/ root (we derive dev root as parent dir).
// repo: primary repo folder name under dev root.
// n: number of agents
// baseBranch: e.g., "main"
// branchPrefix: e.g., "agent"
// dry: log only without changing state
func Setup(devkitRoot, repo string, n int, baseBranch, branchPrefix string, dry bool) error {
	devRoot := filepath.Clean(filepath.Join(devkitRoot, ".."))
	repoPath := filepath.Join(devRoot, repo)
	// Host git should not inherit container SSH settings; force ssh
	envGit := func(args ...string) []string {
		return append([]string{"-u", "GIT_SSH_COMMAND", "git", "-c", "core.sshCommand=ssh"}, args...)
	}

	if err := run(dry, "env", envGit("-C", repoPath, "fetch", "--all", "--prune")...); err != nil {
		return err
	}
	if err := run(dry, "env", envGit("-C", repoPath, "config", "push.default", "upstream")...); err != nil {
		return err
	}
	if err := run(dry, "env", envGit("-C", repoPath, "config", "worktree.useRelativePaths", "true")...); err != nil {
		return err
	}
	// agent1 uses primary path
	b1 := fmt.Sprintf("%s1", branchPrefix)
	if err := run(dry, "env", envGit("-C", repoPath, "checkout", "-B", b1)...); err != nil {
		return err
	}
	if err := run(dry, "env", envGit("-C", repoPath, "branch", "--set-upstream-to=origin/"+baseBranch, b1)...); err != nil {
		return err
	}

	worktreesRoot := filepath.Join(devRoot, paths.AgentWorktreesDir)
	if !dry {
		_ = os.MkdirAll(worktreesRoot, 0755)
	}

	for i := 2; i <= n; i++ {
		parent := filepath.Join(worktreesRoot, fmt.Sprintf("%s%d", branchPrefix, i))
		if !dry {
			_ = os.MkdirAll(parent, 0755)
		}
		wt := filepath.Join(parent, repo)
		bi := fmt.Sprintf("%s%d", branchPrefix, i)
		_ = run(dry, "env", envGit("-C", repoPath, "worktree", "prune")...)            // best effort
		_ = run(dry, "env", envGit("-C", repoPath, "worktree", "remove", "-f", wt)...) // best effort
		if err := run(dry, "env", envGit("-C", repoPath, "worktree", "add", wt, "-B", bi, "origin/"+baseBranch)...); err != nil {
			return err
		}
		if !dry {
			rewriteGitdir(wt)
		}
		if err := run(dry, "env", envGit("-C", wt, "branch", "--set-upstream-to=origin/"+baseBranch, bi)...); err != nil {
			return err
		}
	}
	return nil
}
