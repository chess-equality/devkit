package layout

import (
	"os"
	"path/filepath"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

type Window struct {
	Index   int    `yaml:"index"`
	Path    string `yaml:"path"`
	Name    string `yaml:"name"`
	Service string `yaml:"service"`
	Project string `yaml:"project"`
}

type File struct {
	Session  string    `yaml:"session"`
	Overlays []Overlay `yaml:"overlays"`
	Windows  []Window  `yaml:"windows"`
}

type Overlay struct {
	Project        string   `yaml:"project"`
	Service        string   `yaml:"service"`
	Count          int      `yaml:"count"`
	Profiles       string   `yaml:"profiles"`
	Build          bool     `yaml:"build"`
	ComposeProject string   `yaml:"compose_project"`
	Network        *Network `yaml:"network"`
	// Optional: when targeting the dev-all overlay, request host-side git worktrees
	// to be prepared before tmux windows are attached. This only applies to
	// overlays where Project == "dev-all".
	Worktrees *Worktrees `yaml:"worktrees"`
}

// Worktrees declares host-side git worktree setup for dev-all.
// If provided under an overlay where project == "dev-all", the CLI will
// create N worktrees for the given repo using the specified base branch and
// branch prefix before applying tmux windows.
type Worktrees struct {
	// Primary repo folder name under the dev root (e.g., "ouroboros-ide" or "dumb-onion-hax").
	Repo string `yaml:"repo"`
	// Number of agents/worktrees to prepare. If 0, falls back to Overlay.Count.
	Count int `yaml:"count"`
	// Base branch to track from origin (e.g., "main"). Optional.
	BaseBranch string `yaml:"base_branch"`
	// Prefix for per-agent branch names (e.g., "agent" -> agent1, agent2, ...). Optional.
	BranchPrefix string `yaml:"branch_prefix"`
}

// Network controls the internal subnet and DNS IP used for an overlay's dev networks.
type Network struct {
	Subnet string `yaml:"subnet"`
	DNSIP  string `yaml:"dns_ip"`
}

func Read(p string) (File, error) {
	var f File
	b, err := os.ReadFile(p)
	if err != nil {
		return f, err
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return f, err
	}
	return f, nil
}

// CleanPath normalizes a subpath into a container path based on overlay project.
// If subpath is absolute, it is returned as-is.
// For dev-all, relative paths resolve under /workspaces/dev.
// For codex, relative paths resolve under /workspace.
func CleanPath(project, subpath string) string {
	if strings.HasPrefix(subpath, "/") {
		return filepath.Clean(subpath)
	}
	switch project {
	case "dev-all":
		return filepath.Join("/workspaces/dev", subpath)
	default:
		return filepath.Join("/workspace", subpath)
	}
}
