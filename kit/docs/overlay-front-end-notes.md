Overlay + Tmux Retrospective (Front‑End Overlay)

Summary of what we changed, why it mattered, and how to operate the system reliably when mixing multiple overlays (dev-all + a Node frontend) under a shared compose project.

What went wrong
- Base image pull: The base compose (`kit/compose.yml`) defines a `dev-agent` with `image: devkit/dev-agent:base`. When bringing up the frontend overlay alone, Compose tried to pull this non-existent image. We fixed the overlay to either override or disable the base service.
- Build context resolution: Compose resolves relative build paths from the first `-f` file (the kit file), not the overlay file. The frontend overlay’s `build.context: .` resolved to `devkit/kit`, not the overlay folder.
- Tmux session naming: Using a colon in the layout session (`devkit:ouro-mixed`) confuses tmux (interpreted as `session:window`). We switched to a safe session name (`devkit_ouro-mixed`).
- Missing frontend window: The frontend service had no long-lived command, so the container exited before tmux attached. Window 7 (“front-1”) then never showed up.
- Git identity failures: Containers didn’t have a configured author identity, breaking `git commit` and sometimes tooling flows.
- SSH friction: Commands that relied on `docker compose --index` broke when container numbers weren’t 1..N (e.g., when replicas jumped to 6..13). SSH config also needed to be proxy‑aware and per‑agent HOME‑aware.

Key changes
- Frontend overlay
  - Disabled inherited `dev-agent` or ignored it explicitly to prevent pulling `devkit/dev-agent:base`.
  - Fixed build paths: `build.context: ../overlays/ouroboros-static-front-end` (relative to kit compose).
  - Added keepalive: `command: ["bash", "-lc", "sleep infinity"]`, `stdin_open: true`, `tty: true` to ensure the container runs for tmux attachment.
  - Declared default service in overlay config: add `service: frontend` to `overlays/ouroboros-static-front-end/devkit.yaml` so CLI commands (exec, attach, ssh-setup, repo-*) target the correct container.

- Layout + tmux windows
  - Service‑aware targeting: tmux windows now resolve the Nth container for the requested service (not hardcoded to `dev-agent`).
  - Project‑aware targeting: for windows that specify a `compose_project`, resolution uses container labels `com.docker.compose.project` and `com.docker.compose.service`.
  - Start‑up polling: window commands poll briefly (up to ~60s) for the target service to come up; if none found, they keep the tmux window open with a clear message instead of silently failing.
  - No duplicate first window: when creating a new session, we create the first window once and add the rest after.
  - Path handling: window `path` is normalized per overlay (`/workspaces/dev/...` for dev‑all; `/workspace` for single‑repo overlays).

- Git identity (no fallback policy)
  - The CLI fails fast unless it can determine both user.name and user.email.
  - Resolution order: `DEVKIT_GIT_USER_NAME`/`DEVKIT_GIT_USER_EMAIL` → `GIT_AUTHOR_*`/`GIT_COMMITTER_*` → host `git config --global user.name/user.email`.
  - When present, we explicitly set `git config --global user.name/user.email` inside the container before attaching.

- SSH setup (per‑agent, proxy‑aware, robust indices)
  - Added resilient container selection (docker ps by label) to avoid reliance on Compose replica indices.
  - `ssh-setup` writes keys, `known_hosts`, and an SSH config that tunnels GitHub via the HTTP proxy:
    - `Host github.com\n HostName ssh.github.com\n Port 443\n ProxyCommand nc -X connect -x tinyproxy:8888 %h %p`
  - Index‑free HOME anchor: `/workspace/.devhome` always points to a container‑unique home under `.devhomes/<container-id>`. All SSH/Git config uses raw (unquoted) absolute or `~` paths so it never depends on replica numbering or shell quoting.
  - Sets `git config --global core.sshCommand 'ssh -F ~/.ssh/config'` under the anchor HOME and scrubs any repo‑local `core.sshCommand` overrides.
  - Every write uses `mkdir -p ~/.ssh && cat > ...` with explicit `chmod` to avoid missing directories.

- Seeding (Codex HOME)
  - Replaced fragile `--index` execs with resilient docker execs by name/label.
  - Maintains per‑agent HOME trees and XDG dirs (`.codex`, `.cache`, `.config`, `.local`).

Operational guidance
- Avoid `:` in tmux session names in layouts. Use `devkit_...` style.
- For overlays that don’t start a long‑lived server, add a keepalive command so tmux can attach (`sleep infinity` is a pragmatic default).
- When adding an overlay with `build:`, make the build context relative to the kit compose file path, not the overlay file.
- For mixed overlays under one compose project (e.g., dev-all + frontend), use `compose_project` in the layout to avoid network/port conflicts and to allow cross‑overlay window creation.
- Ensure git identity ahead of time:
  - `export DEVKIT_GIT_USER_NAME="Your Name"`
  - `export DEVKIT_GIT_USER_EMAIL="you@example.com"`
- If containers don’t show up under the expected project/service, validate with:
  - `docker compose -p <project> ps`
  - `docker ps --filter label=com.docker.compose.project=<project> --format '{{.Names}}\t{{.Label "com.docker.compose.service"}}'`

Troubleshooting tips
- Window missing for a service:
  - Check the container exists and is running; if not, build context or command may be wrong.
  - The tmux window will show a message if no container was found within ~60s.
- Base image pull errors:
  - Disable or override inherited services in overlays to prevent Compose from pulling unintended images.
- Network/DNS conflicts:
  - Pick a non‑conflicting subnet and DNS IP: `DEVKIT_INTERNAL_SUBNET`, `DEVKIT_DNS_IP`.
- SSH failures:
  - Re‑run `ssh-setup --index N` and confirm banner with `ssh-test N`.

Reference changes (code & config)
- `overlays/ouroboros-static-front-end/compose.override.yml`:
  - build.context fixed; added keepalive, `stdin_open`, `tty`.
- `cli/devctl/main.go`:
  - Enforced git identity; service‑aware, project‑aware container selection; polling for containers; resilient exec helpers for seeding/ssh/windows.
  - Centralized HOME anchoring via `seed.BuildAnchorScripts` so Codex credentials are reseeded whenever `/workspace/.devhome` moves.
- `kit/docs/README.md`:
  - Documented required env vars for git identity.

Open questions / follow‑ups
- Add a `tmux-add` that accepts `--project` and `--service` to add one window without reapplying a full layout.
- Consider a general “keepalive” flag per overlay service to avoid repeating `sleep infinity` in compose.
