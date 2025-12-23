package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

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
	EnvFiles  []string          `yaml:"env_files"`
	Hooks     Hooks             `yaml:"hooks"`
	Defaults  Defaults          `yaml:"defaults"`
	// Default service name for this overlay (e.g., dev-agent, frontend)
	Service string `yaml:"service"`
	// Optional HTTPS ingress configuration for this overlay.
	Ingress *IngressConfig `yaml:"ingress"`
}

// IngressConfig describes an optional HTTPS reverse proxy service attached to an overlay.
type IngressConfig struct {
	Kind   string            `yaml:"kind"`
	Config string            `yaml:"config"`
	Routes []IngressRoute    `yaml:"routes"`
	Certs  []IngressCert     `yaml:"certs"`
	Hosts  []string          `yaml:"hosts"`
	Env    map[string]string `yaml:"env"`
}

// IngressRoute describes a single host → service:port mapping for generated configs.
type IngressRoute struct {
	Host    string `yaml:"host"`
	Service string `yaml:"service"`
	Port    int    `yaml:"port"`
	Cert    string `yaml:"cert"`
	Key     string `yaml:"key"`
}

// IngressCert points at a certificate or key file to mount into the ingress container.
type IngressCert struct {
	Path string `yaml:"path"`
}

// ReadHooks parses overlays/<project>/devkit.yaml and returns warm/maintain hooks.
// It ignores other fields for backwards compatibility with existing callers.
func ReadHooks(overlays []string, project string) (Hooks, error) {
	cfg, _, _ := ReadAll(overlays, project)
	return cfg.Hooks, nil
}

// ReadAll parses overlays/<project>/devkit.yaml and returns the full overlay config.
func ReadAll(overlays []string, project string) (OverlayConfig, string, error) {
	var out OverlayConfig
	if strings.TrimSpace(project) == "" {
		return out, "", nil
	}
	for _, root := range overlays {
		candidate := filepath.Join(root, project, "devkit.yaml")
		data, err := os.ReadFile(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return out, "", err
		}
		if err := yaml.Unmarshal(data, &out); err != nil {
			return out, filepath.Dir(candidate), err
		}
		if out.Env == nil {
			out.Env = map[string]string{}
		}
		return out, filepath.Dir(candidate), nil
	}
	return out, "", nil
}

// ResolveWorkspace returns the absolute workspace path for an overlay configuration.
// If no workspace is configured, it returns an empty string.
func ResolveWorkspace(cfg OverlayConfig, overlayDir string, root string) string {
	ws := strings.TrimSpace(cfg.Workspace)
	if ws == "" {
		return ""
	}
	resolved := ws
	if !filepath.IsAbs(ws) {
		base := strings.TrimSpace(overlayDir)
		if base == "" {
			base = root
		}
		resolved = filepath.Clean(filepath.Join(base, ws))
	}
	return resolved
}
