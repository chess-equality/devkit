package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Root     string
	Kit      string
	Overlays string
}

func DetectPathsFromExe(exePath string) (Paths, error) {
	root := os.Getenv("DEVKIT_ROOT")
	if root == "" {
		// Binary is expected under devkit/kit/bin/devctl
		binDir := filepath.Dir(exePath)
		root = filepath.Clean(filepath.Join(binDir, "..", ".."))
	}
	root = filepath.Clean(root)
	kit := filepath.Join(root, "kit")
	overlayOverride := strings.TrimSpace(os.Getenv("DEVKIT_OVERLAYS_DIR"))
	var overlays string
	if overlayOverride != "" {
		if filepath.IsAbs(overlayOverride) {
			overlays = filepath.Clean(overlayOverride)
		} else {
			overlays = filepath.Join(root, overlayOverride)
		}
	} else {
		overlays = filepath.Join(root, "overlays")
	}
	return Paths{Root: root, Kit: kit, Overlays: overlays}, nil
}

// Files builds docker compose -f arguments based on profiles and overlay presence.
func Files(p Paths, project, profile string) ([]string, error) {
	var args []string
	base := filepath.Join(p.Kit, "compose.yml")
	args = append(args, "-f", base)

	eff := strings.TrimSpace(profile)
	if eff == "" {
		eff = "dns" // default, matching current bash script
	}
	if eff != "" {
		for _, part := range strings.Split(eff, ",") {
			switch strings.TrimSpace(part) {
			case "hardened":
				args = append(args, "-f", filepath.Join(p.Kit, "compose.hardened.yml"))
			case "dns":
				args = append(args, "-f", filepath.Join(p.Kit, "compose.dns.yml"))
			case "envoy":
				args = append(args, "-f", filepath.Join(p.Kit, "compose.envoy.yml"))
			case "pool":
				args = append(args, "-f", filepath.Join(p.Kit, "compose.pool.yml"))
			case "":
				// skip
			default:
				return nil, fmt.Errorf("unknown profile: %s", part)
			}
		}
	}

	if project != "" {
		overlay := filepath.Join(p.Overlays, project, "compose.override.yml")
		if fileExists(overlay) {
			args = append(args, "-f", overlay)
		}
	}
	return args, nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// AllProfilesFiles returns -f args including all profiles (hardened,dns,envoy) and overlay override if present.
func AllProfilesFiles(p Paths, project string) []string {
	args := []string{"-f", filepath.Join(p.Kit, "compose.yml")}
	args = append(args, "-f", filepath.Join(p.Kit, "compose.hardened.yml"))
	args = append(args, "-f", filepath.Join(p.Kit, "compose.dns.yml"))
	args = append(args, "-f", filepath.Join(p.Kit, "compose.envoy.yml"))
	if project != "" {
		overlay := filepath.Join(p.Overlays, project, "compose.override.yml")
		if fileExists(overlay) {
			args = append(args, "-f", overlay)
		}
	}
	return args
}
