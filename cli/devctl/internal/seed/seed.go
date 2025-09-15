package seed

import (
	"fmt"
	"strings"
)

// WaitForHostMountsScript returns a script that waits (up to ~10s) for
// /var/host-codex or /var/auth.json to become available.
func WaitForHostMountsScript() string {
	return `for i in $(seq 1 20); do { [ -d /var/host-codex ] || [ -f /var/auth.json ]; } && break || sleep 0.5; done`
}

// ResetAndCreateDirsScript resets $HOME/.codex and ensures auxiliary dirs exist.
func ResetAndCreateDirsScript(home string) string {
	h := home
	return `rm -rf '` + h + `/.codex' && mkdir -p '` + h + `/.codex' '` + h + `/.codex/rollouts' '` + h + `/.cache' '` + h + `/.config' '` + h + `/.local'`
}

// CloneHostCodexScript clones the entire /var/host-codex into $HOME/.codex (if present).
func CloneHostCodexScript(home string) string {
	h := home
	return `if [ -d /var/host-codex ]; then cp -a /var/host-codex/. '` + h + `/.codex/'; fi`
}

// FallbackCopyAuthScript copies /var/auth.json into $HOME/.codex/auth.json if still missing.
func FallbackCopyAuthScript(home string) string {
	h := home
	return `if [ ! -f '` + h + `/.codex/auth.json' ] && [ -r /var/auth.json ]; then cp -f /var/auth.json '` + h + `/.codex/auth.json'; fi`
}

// TightenPermsScript chmods 600 on $HOME/.codex/auth.json if present.
func TightenPermsScript(home string) string {
	h := home
	return `if [ -f '` + h + `/.codex/auth.json' ]; then chmod 600 '` + h + `/.codex/auth.json'; fi`
}

// BuildSeedScripts returns a sequence of small bash scripts that, when run
// inside the agent container (via `bash -lc`), refresh the per‑agent Codex HOME
// from host mounts.
func BuildSeedScripts(home string) []string {
	return []string{
		WaitForHostMountsScript(),
		ResetAndCreateDirsScript(home),
		CloneHostCodexScript(home),
		FallbackCopyAuthScript(home),
		TightenPermsScript(home),
	}
}

// AnchorConfig describes how to anchor HOME for a container and optionally seed Codex.
type AnchorConfig struct {
	// Anchor is the symlink path exposed to tooling, e.g. /workspace/.devhome.
	Anchor string
	// Base is the directory holding per-container homes, e.g. /workspace/.devhomes.
	Base string
	// SeedCodex indicates whether Codex credentials should be copied after relinking.
	SeedCodex bool
}

// BuildAnchorScripts returns bash snippets that (1) ensure the anchor symlink points at the
// container-unique directory and (2) optionally seed Codex credentials beneath it.
func BuildAnchorScripts(cfg AnchorConfig) []string {
	anchor := strings.TrimSpace(cfg.Anchor)
	base := strings.TrimSpace(cfg.Base)
	if anchor == "" || base == "" {
		return nil
	}
	script := fmt.Sprintf(
		"cid=$(hostname); target=\"%s\"/\"$cid\"; mkdir -p \"$target/.ssh\" \"$target/.codex/rollouts\" \"$target/.cache\" \"$target/.config\" \"$target/.local\"; chmod 700 \"$target/.ssh\"; ln -sfn \"$target\" \"%s\"",
		base, anchor,
	)
	steps := []string{script}
	if cfg.SeedCodex {
		steps = append(steps, BuildSeedScripts(anchor)...)
	}
	return steps
}

// JoinScripts joins bash snippets with a " ; " delimiter, trimming whitespace.
func JoinScripts(scripts []string) string {
	parts := make([]string, 0, len(scripts))
	for _, sc := range scripts {
		s := strings.TrimSpace(sc)
		if s == "" {
			continue
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}
