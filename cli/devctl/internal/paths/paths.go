package paths

import (
	"path/filepath"
	"strings"
)

// AgentWorktreesDir is the host/container subdirectory where additional agent
// worktrees live (agent2, agent3, ...).
const AgentWorktreesDir = "agent-worktrees"

// AgentRepoPath returns the working directory path inside the container
// for a given project overlay, agent index, and repo name.
// - dev-all: /workspaces/dev/<repo> or /workspaces/dev/agentN/<repo>
// - codex  : /workspace
func AgentRepoPath(project, idx, repo string) string {
	if project == "dev-all" {
		base := "/workspaces/dev"
		if idx == "1" {
			return filepath.Join(base, repo)
		}
		return filepath.Join(base, AgentWorktreesDir, "agent"+idx, repo)
	}
	// codex overlay (single mount at /workspace)
	return "/workspace"
}

// AgentHomePath returns the per-agent HOME inside the container for a project/index/repo.
//   - dev-all: agent1 -> /workspaces/dev/<repo>/.devhome-agent1
//     agentN -> /workspaces/dev/agent-worktrees/agentN/.devhome-agentN
//   - codex  : /workspace/.devhome-agentN
func AgentHomePath(project, idx, repo string) string {
	if strings.TrimSpace(project) == "dev-all" {
		base := "/workspaces/dev"
		suffix := ".devhome-agent" + strings.TrimSpace(idx)
		if strings.TrimSpace(idx) == "1" {
			repo = strings.TrimSpace(repo)
			if repo != "" {
				return filepath.Join(base, repo, suffix)
			}
			// Fallback for callers that don't provide a repo name.
			return filepath.Join(base, ".devhomes", "agent"+strings.TrimSpace(idx))
		}
		return filepath.Join(base, AgentWorktreesDir, "agent"+strings.TrimSpace(idx), suffix)
	}
	return filepath.Join("/workspace", ".devhome-agent"+strings.TrimSpace(idx))
}

// AgentEnv returns HOME and related XDG/Codex variables for the agent.
func AgentEnv(project, idx, repo string) map[string]string {
	home := AgentHomePath(project, idx, repo)
	return map[string]string{
		"HOME":              home,
		"CODEX_HOME":        filepath.Join(home, ".codex"),
		"CODEX_ROLLOUT_DIR": filepath.Join(home, ".codex", "rollouts"),
		"XDG_CACHE_HOME":    filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
	}
}
