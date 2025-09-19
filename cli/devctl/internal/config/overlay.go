package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Hooks struct {
	Warm     string
	Maintain string
}

type Defaults struct {
	// Default number of agents to start (for reset/bootstrap flows)
	Agents int `yaml:"agents"`
	// Default repo name under the dev root (e.g., ouroboros-ide)
	Repo string `yaml:"repo"`
	// Base branch to track from origin (e.g., main)
	BaseBranch string `yaml:"base_branch"`
	// Prefix for per-agent branch names (e.g., agent -> agent1, agent2, ...)
	BranchPrefix string `yaml:"branch_prefix"`
	// Default compose profiles to apply (comma-separated)
	Profiles string `yaml:"profiles"`
}

type OverlayConfig struct {
	Workspace string            `yaml:"workspace"`
	Env       map[string]string `yaml:"env"`
	Hooks     Hooks             `yaml:"hooks"`
	Defaults  Defaults          `yaml:"defaults"`
	// Default service name for this overlay (e.g., dev-agent, frontend)
	Service string `yaml:"service"`
}

// ReadHooks parses overlays/<project>/devkit.yaml and returns warm/maintain hooks.
// It ignores other fields for backwards compatibility with existing callers.
func ReadHooks(overlaysRoot, project string) (Hooks, error) {
	cfg, _ := ReadAll(overlaysRoot, project)
	return cfg.Hooks, nil
}

// ReadAll parses overlays/<project>/devkit.yaml and returns the full overlay config.
func ReadAll(overlaysRoot, project string) (OverlayConfig, error) {
	var out OverlayConfig
	if project == "" {
		return out, nil
	}
	path := filepath.Join(overlaysRoot, project, "devkit.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return out, nil
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return out, nil
	}
	return out, nil
}
