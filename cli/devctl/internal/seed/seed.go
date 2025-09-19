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
// container-unique directory and (2) optionally seed Codex credentials beneath it. The seeding
// work operates directly on the resolved target (instead of the shared symlink) so multiple
// containers can run it concurrently without clobbering each other's state.
func BuildAnchorScripts(cfg AnchorConfig) []string {
	anchor := strings.TrimSpace(cfg.Anchor)
	base := strings.TrimSpace(cfg.Base)
	if anchor == "" || base == "" {
		return nil
	}
	parts := []string{
		"set -e",
		fmt.Sprintf("target=\"%s/$(hostname)\"", base),
		"mkdir -p \"$target/.ssh\" \"$target/.cache\" \"$target/.config\" \"$target/.local\"",
		"chmod 700 \"$target/.ssh\"",
		"dev_home_ok=0; if mkdir -p /home/dev 2>/dev/null; then dev_home_ok=1; elif [ -d /home/dev ]; then dev_home_ok=1; fi",
		fmt.Sprintf("ln -sfn \"$target\" %s", shQuote(anchor)),
		"mkdir -p \"$target/.sbt\"",
		"if [ \"$dev_home_ok\" = 1 ]; then if [ -d /home/dev/.ivy2 ]; then ln -sfn /home/dev/.ivy2 \"$target/.ivy2\"; fi; fi",
		"if [ \"$dev_home_ok\" = 1 ]; then if [ -e /home/dev/.sbt ] || [ -L /home/dev/.sbt ]; then rm -rf /home/dev/.sbt; fi; ln -sfn \"$target/.sbt\" /home/dev/.sbt; fi",
		"if [ \"$dev_home_ok\" = 1 ]; then if [ -d /home/dev/.cache/coursier ]; then ln -sfn /home/dev/.cache/coursier \"$target/.cache/coursier\"; fi; fi",
		"if [ \"$dev_home_ok\" = 1 ] && [ -n \"${DOCKER_HOST:-}\" ]; then printf \"docker.host=%s\\n\" \"$DOCKER_HOST\" > \"$target/.testcontainers.properties\"; ln -sfn \"$target/.testcontainers.properties\" /home/dev/.testcontainers.properties; fi",
	}
	if cfg.SeedCodex {
		seedSteps := []string{
			WaitForHostMountsScript(),
			"rm -rf \"$target/.codex\"",
			"mkdir -p \"$target/.codex\" \"$target/.codex/rollouts\" \"$target/.cache\" \"$target/.config\" \"$target/.local\"",
			"if [ -d /var/host-codex ]; then cp -a /var/host-codex/. \"$target/.codex/\"; fi",
			"if [ ! -f \"$target/.codex/auth.json\" ] && [ -r /var/auth.json ]; then cp -f /var/auth.json \"$target/.codex/auth.json\"; fi",
			"if [ -f \"$target/.codex/auth.json\" ]; then chmod 600 \"$target/.codex/auth.json\"; fi",
			"touch \"$marker\"",
		}
		parts = append(parts,
			"marker=\"$target/.codex/.seeded\"",
			"if [ ! -f \"$marker\" ]; then "+strings.Join(seedSteps, "; ")+"; fi",
		)
	}
	return []string{strings.Join(parts, "; ")}
}

// shQuote provides the minimal quoting needed for simple POSIX-safe paths.
func shQuote(path string) string {
	if !strings.ContainsAny(path, " '\"$") {
		return fmt.Sprintf("\"%s\"", path)
	}
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "\\$", "`", "\\`")
	return "\"" + replacer.Replace(path) + "\""
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
