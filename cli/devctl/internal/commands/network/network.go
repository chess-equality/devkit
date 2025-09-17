package network

import (
	"fmt"
	"strings"

	"devkit/cli/devctl/internal/cmdregistry"
	runner "devkit/cli/devctl/internal/runner"
)

// Register adds proxy/check commands to the registry.
func Register(r *cmdregistry.Registry) {
	r.Register("proxy", handleProxy)
	r.Register("check-net", handleCheckNet)
	r.Register("check-codex", handleCheckCodex)
}

func handleProxy(ctx *cmdregistry.Context) error {
	project := strings.TrimSpace(ctx.Project)
	if project == "" {
		return fmt.Errorf("-p <project> is required")
	}
	which := "tinyproxy"
	if len(ctx.Args) > 0 && strings.TrimSpace(ctx.Args[0]) != "" {
		which = strings.TrimSpace(ctx.Args[0])
	}
	switch which {
	case "tinyproxy":
		fmt.Println("Switching agent env to tinyproxy... (ensure overlay uses HTTP(S)_PROXY=http://tinyproxy:8888)")
	case "envoy":
		fmt.Println("Enable envoy profile: add --profile envoy to up/restart commands")
	default:
		return fmt.Errorf("unknown proxy: %s", which)
	}
	return nil
}

func handleCheckNet(ctx *cmdregistry.Context) error {
	project := strings.TrimSpace(ctx.Project)
	if project == "" {
		return fmt.Errorf("-p <project> is required")
	}
	script := "set -x; env | grep -E 'HTTP(S)?_PROXY|NO_PROXY'; curl -Is https://github.com | head -n1; (curl -Is https://example.com | head -n1 || true)"
	runner.Compose(ctx.DryRun, ctx.Files, "exec", "dev-agent", "bash", "-lc", script)
	return nil
}

func handleCheckCodex(ctx *cmdregistry.Context) error {
	project := strings.TrimSpace(ctx.Project)
	if project == "" {
		return fmt.Errorf("-p <project> is required")
	}
	fmt.Println("== Env vars ==")
	runner.Compose(ctx.DryRun, ctx.Files, "exec", "dev-agent", "bash", "-lc", "env | grep -E '^HTTPS?_PROXY=|^NO_PROXY=' || true")
	fmt.Println("== Curl checks (through proxy) ==")
	runner.Compose(ctx.DryRun, ctx.Files, "exec", "dev-agent", "bash", "-lc", "set -e; echo -n 'chatgpt.com          : '; curl -sSvo /dev/null -w '%{http_code}\\n' https://chatgpt.com || true")
	runner.Compose(ctx.DryRun, ctx.Files, "exec", "dev-agent", "bash", "-lc", "set -e; echo -n 'chatgpt.com/backend..: '; curl -sSvo /dev/null -w '%{http_code}\\n' https://chatgpt.com/backend-api/codex/responses || true")
	runner.Compose(ctx.DryRun, ctx.Files, "exec", "dev-agent", "bash", "-lc", "mkdir -p /workspace/.devhome; HOME=/workspace/.devhome CODEX_HOME=/workspace/.devhome/.codex timeout 15s codex exec 'Reply with: ok' || true")
	return nil
}
