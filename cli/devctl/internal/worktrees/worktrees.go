package worktrees

import (
	"context"
	"devkit/cli/devctl/internal/execx"
	"devkit/cli/devctl/internal/paths"
	"errors"
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

// cleanWorktreePath removes the target directory when it is clearly stale:
// missing .git metadata, pointing to a different repository, or referencing a
// gitdir that no longer exists. We keep the directory when it still looks like
// a valid worktree for the given repo.
func cleanWorktreePath(repoWorktreesDir, wt string) error {
	info, err := os.Stat(wt)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return os.RemoveAll(wt)
	}
	gitFile := filepath.Join(wt, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.RemoveAll(wt)
		}
		return err
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir:") {
		return os.RemoveAll(wt)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(content, "gitdir:"))
	if gitdir == "" {
		return os.RemoveAll(wt)
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Clean(filepath.Join(wt, gitdir))
	}
	resolved, err := filepath.EvalSymlinks(gitdir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.RemoveAll(wt)
		}
		return err
	}
	if repoWorktreesDir != "" {
		rel, err := filepath.Rel(repoWorktreesDir, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			return os.RemoveAll(wt)
		}
	}
	if _, err := os.Stat(resolved); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.RemoveAll(wt)
		}
		return err
	}
	return nil
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
	gitDirty := func(path string) (bool, error) {
		out, res := execx.Capture(context.Background(), "git", "-C", path, "status", "--porcelain")
		if res.Code != 0 {
			return false, fmt.Errorf("git status --porcelain %s: exit %d", path, res.Code)
		}
		return strings.TrimSpace(out) != "", nil
	}
	warnDirty := func(path string) {
		fmt.Fprintf(os.Stderr, "[worktrees] warning: uncommitted changes in %s; leaving worktree intact\n", path)
	}

	var repoWorktreesDir string
	if !dry {
		if out, res := execx.Capture(context.Background(), "git", "-C", repoPath, "rev-parse", "--git-dir"); res.Code == 0 {
			gitdir := strings.TrimSpace(out)
			if gitdir != "" {
				if !filepath.IsAbs(gitdir) {
					gitdir = filepath.Clean(filepath.Join(repoPath, gitdir))
				}
				if resolved, err := filepath.EvalSymlinks(gitdir); err == nil {
					gitdir = resolved
				}
				repoWorktreesDir = filepath.Join(gitdir, "worktrees")
				if resolved, err := filepath.EvalSymlinks(repoWorktreesDir); err == nil {
					repoWorktreesDir = resolved
				} else {
					repoWorktreesDir = filepath.Clean(repoWorktreesDir)
				}
			}
		}
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
	if !dry {
		dirty, err := gitDirty(repoPath)
		if err != nil {
			return err
		}
		if dirty {
			warnDirty(repoPath)
		} else {
			if err := run(dry, "env", envGit("-C", repoPath, "checkout", "-B", b1, "origin/"+baseBranch)...); err != nil {
				return err
			}
			if err := run(dry, "env", envGit("-C", repoPath, "branch", "--set-upstream-to=origin/"+baseBranch, b1)...); err != nil {
				return err
			}
		}
	} else {
		if err := run(dry, "env", envGit("-C", repoPath, "checkout", "-B", b1, "origin/"+baseBranch)...); err != nil {
			return err
		}
		if err := run(dry, "env", envGit("-C", repoPath, "branch", "--set-upstream-to=origin/"+baseBranch, b1)...); err != nil {
			return err
		}
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
		if !dry {
			if err := cleanWorktreePath(repoWorktreesDir, wt); err != nil {
				return err
			}
			if _, statErr := os.Stat(wt); statErr == nil {
				dirty, err := gitDirty(wt)
				if err != nil {
					return err
				}
				if dirty {
					warnDirty(wt)
					continue
				}
			} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		}
		_ = run(dry, "env", envGit("-C", repoPath, "worktree", "prune")...)            // best effort
		_ = run(dry, "env", envGit("-C", repoPath, "worktree", "remove", "-f", wt)...) // best effort
		if err := run(dry, "env", envGit("-C", repoPath, "worktree", "add", wt, "-B", bi, "origin/"+baseBranch)...); err != nil {
			if dry {
				return err
			}
			if remErr := os.RemoveAll(wt); remErr != nil && !errors.Is(remErr, os.ErrNotExist) {
				return fmt.Errorf("remove stale worktree %s: %w", wt, remErr)
			}
			if err2 := run(dry, "env", envGit("-C", repoPath, "worktree", "add", wt, "-B", bi, "origin/"+baseBranch)...); err2 != nil {
				return err2
			}
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
