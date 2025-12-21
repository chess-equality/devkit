package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devkit/cli/devctl/internal/config"
	"devkit/cli/devctl/internal/ingress"
)

type Paths struct {
	Root         string
	Kit          string
	OverlayPaths []string
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
	var overlays []string
	if overlayOverride != "" {
		overlays = append(overlays, splitOverlayPaths(root, overlayOverride)...)
	} else {
		overlays = append(overlays, filepath.Join(root, "overlays"))
	}
	return Paths{Root: root, Kit: kit, OverlayPaths: uniquePaths(overlays)}, nil
}

// Files builds docker compose -f arguments based on profiles and overlay presence.
func Files(p Paths, project, profile string) ([]string, error) {
	var args []string
	base := filepath.Join(p.Kit, "compose.yml")
	args = append(args, "-f", base)

	var overlayCfg config.OverlayConfig
	var overlayDir string
	var cfgErr error
	if project != "" {
		overlayCfg, overlayDir, cfgErr = config.ReadAll(p.OverlayPaths, project)
		if cfgErr != nil {
			return nil, cfgErr
		}
		if ws := config.ResolveWorkspace(overlayCfg, overlayDir, p.Root); ws != "" {
			if err := ensureWorkspaceWritable(ws); err != nil {
				return nil, err
			}
		}
	}

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
		if overlay := findOverlayFile(p.OverlayPaths, project, "compose.override.yml"); overlay != "" {
			args = append(args, "-f", overlay)
		}
		if overlayCfg.Ingress != nil {
			frag, err := ingress.BuildFragment(project, overlayCfg.Ingress, overlayDir, p.Root)
			if err != nil {
				return nil, err
			}
			if frag.Path != "" {
				args = append(args, "-f", frag.Path)
			}
		}
	}
	return args, nil
}

func ensureWorkspaceWritable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workspace directory %s does not exist", path)
		}
		return fmt.Errorf("workspace directory %s not accessible: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace directory %s is not a directory", path)
	}
	f, err := os.CreateTemp(path, ".devkit-workspace-check-*")
	if err != nil {
		return fmt.Errorf("workspace directory %s not writable: %w", path, err)
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("workspace directory %s cleanup failed: %w", path, err)
	}
	return nil
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
		if overlay := findOverlayFile(p.OverlayPaths, project, "compose.override.yml"); overlay != "" {
			args = append(args, "-f", overlay)
		}
	}
	return args
}

func splitOverlayPaths(root, override string) []string {
	parts := strings.Split(override, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, raw := range parts {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if !filepath.IsAbs(v) {
			v = filepath.Join(root, v)
		}
		out = append(out, filepath.Clean(v))
	}
	return out
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		c := filepath.Clean(p)
		if !seen[c] {
			seen[c] = true
			result = append(result, c)
		}
	}
	return result
}

func findOverlayFile(paths []string, project, name string) string {
	for _, root := range paths {
		candidate := filepath.Join(root, project, name)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// FindOverlayDir returns the first directory containing the given overlay project.
func FindOverlayDir(paths []string, project string) string {
	if strings.TrimSpace(project) == "" {
		return ""
	}
	for _, root := range paths {
		candidate := filepath.Join(root, project)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
	}
	return ""
}

// MergeOverlayPaths appends extra overlay search roots, preserving order and removing duplicates.
func MergeOverlayPaths(base []string, extra ...string) []string {
	combined := append([]string{}, base...)
	combined = append(combined, extra...)
	return uniquePaths(combined)
}
