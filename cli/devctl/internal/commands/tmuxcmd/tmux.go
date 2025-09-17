package tmuxcmd

import (
	"fmt"
	"strings"

	"devkit/cli/devctl/internal/cmdregistry"
	"devkit/cli/devctl/internal/compose"
	"devkit/cli/devctl/internal/layout"
	runner "devkit/cli/devctl/internal/runner"
	"devkit/cli/devctl/internal/tmuxutil"
)

// Register adds tmux-related commands to the registry. Helpers from the legacy
// main.go are injected so we can reuse them without moving everything at once.
func Register(
	r *cmdregistry.Registry,
	syncFn func(bool, compose.Paths, string, []string, string, string, string, int, string),
	ensureWindowFn func(bool, compose.Paths, string, []string, string, string, string, string, string),
	defaultSessionFn func(string) string,
	atoiFn func(string) int,
	listFn func([]string, string) []string,
	buildFn func([]string, string, string, string, string) string,
	hasFn func(string) bool,
) {
	doSyncTmux = syncFn
	ensureTmuxSessionWithWindow = ensureWindowFn
	defaultSessionName = defaultSessionFn
	mustAtoi = atoiFn
	listServiceNames = listFn
	buildWindowCmd = buildFn
	hasTmuxSession = hasFn

	r.Register("tmux-sync", handleSync)
	r.Register("tmux-add-cd", handleAddCD)
	r.Register("tmux-apply-layout", handleApplyLayout)
}

func handleSync(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	sessName := ""
	count := 0
	namePrefix := "agent-"
	cdPath := ""
	service := "dev-agent"
	for i := 0; i < len(ctx.Args); i++ {
		switch ctx.Args[i] {
		case "--session":
			if i+1 < len(ctx.Args) {
				sessName = ctx.Args[i+1]
				i++
			}
		case "--count":
			if i+1 < len(ctx.Args) {
				count = mustAtoi(ctx.Args[i+1])
				i++
			}
		case "--name-prefix":
			if i+1 < len(ctx.Args) {
				namePrefix = ctx.Args[i+1]
				i++
			}
		case "--cd":
			if i+1 < len(ctx.Args) {
				cdPath = ctx.Args[i+1]
				i++
			}
		case "--service":
			if i+1 < len(ctx.Args) {
				service = ctx.Args[i+1]
				i++
			}
		}
	}
	if count <= 0 {
		count = len(listServiceNames(ctx.Files, service))
		if count == 0 {
			return fmt.Errorf("no dev-agent containers running; use up/scale first or provide --count")
		}
	}
	doSyncTmux(ctx.DryRun, ctx.Paths, ctx.Project, ctx.Files, sessName, namePrefix, cdPath, count, service)
	return nil
}

func handleAddCD(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	if len(ctx.Args) < 2 {
		return fmt.Errorf("Usage: tmux-add-cd <index> <subpath> [--session NAME] [--name NAME] [--service NAME]")
	}
	idx := ctx.Args[0]
	subpath := ctx.Args[1]
	sessName := ""
	winName := ""
	service := "dev-agent"
	for i := 2; i < len(ctx.Args); i++ {
		switch ctx.Args[i] {
		case "--session":
			if i+1 < len(ctx.Args) {
				sessName = ctx.Args[i+1]
				i++
			}
		case "--name":
			if i+1 < len(ctx.Args) {
				winName = ctx.Args[i+1]
				i++
			}
		case "--service":
			if i+1 < len(ctx.Args) {
				service = ctx.Args[i+1]
				i++
			}
		}
	}
	if sessName == "" {
		sessName = defaultSessionName(ctx.Project)
	}
	ensureTmuxSessionWithWindow(ctx.DryRun, ctx.Paths, ctx.Project, ctx.Files, sessName, idx, subpath, winName, service)
	return nil
}

func handleApplyLayout(ctx *cmdregistry.Context) error {
	if err := ensureProject(ctx); err != nil {
		return err
	}
	layoutPath := ""
	sessName := ""
	doAttach := false
	for i := 0; i < len(ctx.Args); i++ {
		switch ctx.Args[i] {
		case "--file":
			if i+1 < len(ctx.Args) {
				layoutPath = ctx.Args[i+1]
				i++
			}
		case "--session":
			if i+1 < len(ctx.Args) {
				sessName = ctx.Args[i+1]
				i++
			}
		case "--attach":
			doAttach = true
		}
	}
	if strings.TrimSpace(layoutPath) == "" {
		return fmt.Errorf("Usage: tmux-apply-layout --file <layout.yaml> [--session NAME]")
	}
	lf, err := layout.Read(layoutPath)
	if err != nil {
		return err
	}
	if sessName == "" {
		if strings.TrimSpace(lf.Session) != "" {
			sessName = lf.Session
		} else {
			sessName = defaultSessionName(ctx.Project)
		}
	}
	sessExists := hasTmuxSession(sessName)
	if !sessExists && len(lf.Windows) > 0 {
		w := lf.Windows[0]
		idx := fmt.Sprintf("%d", w.Index)
		winProj := ctx.Project
		if strings.TrimSpace(w.Project) != "" {
			winProj = w.Project
		}
		fargs, err := compose.Files(ctx.Paths, winProj, ctx.Profile)
		if err != nil {
			return err
		}
		dest := layout.CleanPath(winProj, w.Path)
		svc := w.Service
		if strings.TrimSpace(svc) == "" {
			svc = "dev-agent"
		}
		name := w.Name
		if strings.TrimSpace(name) == "" {
			name = "agent-" + idx
		}
		cmdStr := buildWindowCmd(fargs, winProj, idx, dest, svc)
		runner.Host(ctx.DryRun, "tmux", tmuxutil.NewSession(sessName, cmdStr)...)
		runner.Host(ctx.DryRun, "tmux", tmuxutil.RenameWindow(sessName+":0", name)...)
		sessExists = true
	}
	for _, w := range lf.Windows {
		idx := fmt.Sprintf("%d", w.Index)
		winProj := ctx.Project
		if strings.TrimSpace(w.Project) != "" {
			winProj = w.Project
		}
		fargs, err := compose.Files(ctx.Paths, winProj, ctx.Profile)
		if err != nil {
			return err
		}
		dest := layout.CleanPath(winProj, w.Path)
		svc := w.Service
		if strings.TrimSpace(svc) == "" {
			svc = "dev-agent"
		}
		name := w.Name
		if strings.TrimSpace(name) == "" {
			name = "agent-" + idx
		}
		cmdStr := buildWindowCmd(fargs, winProj, idx, dest, svc)
		runner.Host(ctx.DryRun, "tmux", tmuxutil.NewWindow(sessName, name, cmdStr)...)
	}
	if doAttach {
		runner.HostInteractive(ctx.DryRun, "tmux", tmuxutil.Attach(sessName)...)
	}
	return nil
}

func ensureProject(ctx *cmdregistry.Context) error {
	if strings.TrimSpace(ctx.Project) == "" {
		return fmt.Errorf("-p <project> is required")
	}
	return nil
}

var (
	doSyncTmux                  func(bool, compose.Paths, string, []string, string, string, string, int, string)
	ensureTmuxSessionWithWindow func(bool, compose.Paths, string, []string, string, string, string, string, string)
	defaultSessionName          func(string) string
	mustAtoi                    func(string) int
	listServiceNames            func([]string, string) []string
	buildWindowCmd              func([]string, string, string, string, string) string
	hasTmuxSession              func(string) bool
)
