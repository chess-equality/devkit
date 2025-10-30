package runner

import (
	"fmt"
	"os"
	"strings"
	"time"

	"devkit/cli/devctl/internal/execx"
)

// Compose runs `docker compose` with the provided -f arguments and subcommand.
// When dry is true it only prints the command to stderr.
func Compose(dry bool, fileArgs []string, args ...string) {
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	all := append([]string{"compose"}, append(fileArgs, args...)...)
	if dry {
		fmt.Fprintln(os.Stderr, "+ docker "+strings.Join(all, " "))
		return
	}
	res := execx.RunCtx(ctx, "docker", all...)
	if res.Code != 0 {
		os.Exit(res.Code)
	}
}

// ComposeInteractive executes docker compose without a timeout for interactive usage.
func ComposeInteractive(dry bool, fileArgs []string, args ...string) {
	all := append([]string{"compose"}, append(fileArgs, args...)...)
	if dry {
		fmt.Fprintln(os.Stderr, "+ docker "+strings.Join(all, " "))
		return
	}
	res := execx.Run("docker", all...)
	if res.Code != 0 {
		os.Exit(res.Code)
	}
}

// ComposeInput runs docker compose with stdin content forwarded to the process.
func ComposeInput(dry bool, fileArgs []string, input []byte, args ...string) {
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	all := append([]string{"compose"}, append(fileArgs, args...)...)
	if dry {
		fmt.Fprintln(os.Stderr, "+ docker "+strings.Join(all, " "))
		return
	}
	res := execx.RunWithInput(ctx, input, "docker", all...)
	if res.Code != 0 {
		os.Exit(res.Code)
	}
}

// ComposeWithProject adds an explicit project name (-p) before running docker compose.
func ComposeWithProject(dry bool, projectName string, fileArgs []string, args ...string) error {
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	all := []string{"compose"}
	if strings.TrimSpace(projectName) != "" {
		all = append(all, "-p", projectName)
	}
	all = append(all, append(fileArgs, args...)...)
	if dry {
		fmt.Fprintln(os.Stderr, "+ docker "+strings.Join(all, " "))
		return nil
	}
	res := execx.RunCtx(ctx, "docker", all...)
	if res.Code != 0 {
		if res.Err != nil {
			return res.Err
		}
		return fmt.Errorf("docker compose exited with code %d", res.Code)
	}
	return nil
}

// Host executes a host binary with a default 10 minute timeout.
func Host(dry bool, name string, args ...string) {
	ctx, cancel := execx.WithTimeout(10 * time.Minute)
	defer cancel()
	if dry {
		fmt.Fprintln(os.Stderr, "+ "+name+" "+strings.Join(args, " "))
		return
	}
	res := execx.RunCtx(ctx, name, args...)
	if res.Code != 0 {
		os.Exit(res.Code)
	}
}

// HostInteractive runs a host command without a timeout (for tmux attach, etc.).
func HostInteractive(dry bool, name string, args ...string) {
	if dry {
		fmt.Fprintln(os.Stderr, "+ "+name+" "+strings.Join(args, " "))
		return
	}
	res := execx.Run(name, args...)
	if res.Code != 0 {
		os.Exit(res.Code)
	}
}

// HostBestEffort executes a host command and ignores non-zero exits.
func HostBestEffort(dry bool, name string, args ...string) {
	ctx, cancel := execx.WithTimeout(2 * time.Minute)
	defer cancel()
	if dry {
		fmt.Fprintln(os.Stderr, "+ "+name+" "+strings.Join(args, " "))
		return
	}
	_ = execx.RunCtx(ctx, name, args...)
}
