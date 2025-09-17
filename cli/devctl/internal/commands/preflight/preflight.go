package preflight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devkit/cli/devctl/internal/cmdregistry"
	"devkit/cli/devctl/internal/config"
	"devkit/cli/devctl/internal/execx"
)

// Register adds the preflight command to the registry.
func Register(r *cmdregistry.Registry) {
	r.Register("preflight", handle)
}

func handle(ctx *cmdregistry.Context) error {
	ok := true
	if _, err := execx.Capture(context.Background(), "docker", "version"); err.Code != 0 {
		fmt.Fprintln(os.Stderr, "[preflight] docker not available or daemon unreachable")
		ok = false
	} else {
		fmt.Println("[preflight] docker: OK")
	}
	if _, err := execx.Capture(context.Background(), "tmux", "-V"); err.Code != 0 {
		fmt.Fprintln(os.Stderr, "[preflight] tmux not found (only needed for tmux windows)")
	} else {
		fmt.Println("[preflight] tmux: OK")
	}
	if home, herr := os.UserHomeDir(); herr == nil {
		codexDir := filepath.Join(home, ".codex")
		if st, er := os.Stat(codexDir); er == nil && st.IsDir() {
			if _, er2 := os.Stat(filepath.Join(codexDir, "auth.json")); er2 == nil {
				fmt.Println("[preflight] ~/.codex: OK (auth.json present)")
			} else {
				fmt.Fprintln(os.Stderr, "[preflight] ~/.codex present but auth.json missing")
				ok = false
			}
		} else {
			fmt.Fprintln(os.Stderr, "[preflight] ~/.codex not found; codex may prompt for login in containers")
		}
		key := filepath.Join(home, ".ssh", "id_ed25519")
		if _, er := os.Stat(key); er != nil {
			key = filepath.Join(home, ".ssh", "id_rsa")
		}
		if st, er := os.Stat(key); er == nil && !st.IsDir() {
			if _, er2 := os.Stat(key + ".pub"); er2 == nil {
				fmt.Println("[preflight] SSH key: OK (", filepath.Base(key), ")")
			} else {
				fmt.Fprintln(os.Stderr, "[preflight] SSH private key found but public key missing:", key+".pub")
			}
		} else {
			fmt.Fprintln(os.Stderr, "[preflight] No SSH private key found (~/.ssh/id_ed25519 or id_rsa)")
		}
	} else {
		fmt.Fprintln(os.Stderr, "[preflight] cannot resolve HOME to check ~/.codex and SSH keys")
	}
	if !ok {
		return fmt.Errorf("preflight checks failed")
	}
	if ctx.Pool.Mode == config.CredModePool {
		dir := strings.TrimSpace(ctx.Pool.Dir)
		if dir == "" {
			return fmt.Errorf("[preflight] DEVKIT_CODEX_POOL_DIR not set but DEVKIT_CODEX_CRED_MODE=pool")
		}
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return fmt.Errorf("[preflight] pool dir not found or not a directory: %s", dir)
		}
		entries, _ := os.ReadDir(dir)
		slots := 0
		for _, e := range entries {
			if e.IsDir() {
				slots++
			}
		}
		if slots == 0 {
			return fmt.Errorf("[preflight] pool dir has no slots (subdirectories): %s", dir)
		}
		fmt.Printf("[preflight] pool: OK (%d slots in %s). Reminder: include --profile pool to mount.\n", slots, dir)
	}
	return nil
}
