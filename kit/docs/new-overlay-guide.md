Creating a New Overlay (Guide + Gotchas)

This guide walks you through creating a new overlay under `devkit/overlays/<your-project>/` and highlights common pitfalls around Compose paths, networking, SSH/Git, and tmux.

1) Files to add (or start from the template)
- Easiest: copy `devkit/overlays/_template/` to `devkit/overlays/<your-overlay>/` and edit paths/names.

Manual steps if not using the template:
- overlays/<name>/devkit.yaml
  - Required: `workspace: ../../<your-repo-folder>` — the CLI resolves it to an absolute `WORKSPACE_DIR` before compose runs.
  - Recommended: `service: <service-name>` to set the default service for CLI exec/attach/ssh/repo commands.
  - Recommended: `defaults:` block describing the repo name (`defaults.repo`), desired agent count (`defaults.agents`), and branch metadata (`defaults.base_branch`, `defaults.branch_prefix`) so runtime-config helpers work without extra flags.
  - Optional: `env:` to provide host defaults (e.g., `AWS_PROFILE`) that users can still override.
  - Optional: `env_files:` pointing at dotenv-style files (paths relative to the overlay directory) to prepopulate env vars without committing secrets.
  - Optional hooks: `warm`, `maintain` (run inside container via `devkit warm|maintain`).
    - Best practice: drop in the python shim so tools that still call `python` work without edits:
      ````
      warm: |
        set -euo pipefail
        mkdir -p /workspace/.local/bin
        if command -v python3 >/dev/null 2>&1 && ! command -v python >/dev/null 2>&1; then
          ln -sf "$(command -v python3)" /workspace/.local/bin/python
        fi
        # your existing warm steps (npm install, sbt update, ...)
      ````

- overlays/<name>/compose.override.yml
  - Define your overlay service (e.g., `frontend`) and join it to the internal network.
  - Important: `build.context` is resolved relative to the FIRST compose file (kit/compose.yml). Use a path relative to `devkit/kit`, not the overlay folder.

Minimal example
```
services:
  # Disable the base dev-agent (kit/compose.yml) so Compose doesn’t try to pull an unknown image
  dev-agent:
    profiles: [disabled]

  frontend:
    build:
      context: ../overlays/<name>   # relative to devkit/kit/compose.yml
      dockerfile: Dockerfile
    image: local/dev-agent:node18-git
    command: ["bash", "-lc", "sleep infinity"]  # keep container alive for tmux/exec
    stdin_open: true
    tty: true
    environment:
      - HTTP_PROXY=http://tinyproxy:8888
      - HTTPS_PROXY=http://tinyproxy:8888
      - NO_PROXY=localhost,127.0.0.1,tinyproxy
    working_dir: /workspace
    depends_on:
      tinyproxy:
        condition: service_healthy
    networks:
      - dev-internal
    volumes:
      - ${WORKSPACE_DIR:-.}:/workspace:rw
      - ${DEVKIT_WORKTREE_ROOT}:${DEVKIT_WORKTREE_CONTAINER_ROOT:-/worktrees}:rw

networks:
  dev-internal:
    external: true  # provided by kit/compose.yml (the base compose defines it)
```

2) Dockerfile tips (proxy + SSH)
- Install basic tools for Git over SSH with proxy:
  - `git`, `openssh-client`, `ca-certificates`, `netcat-openbsd` (required for `ProxyCommand nc ...`).
```
RUN apt-get update && apt-get install -y --no-install-recommends \
      git openssh-client ca-certificates netcat-openbsd \
    && rm -rf /var/lib/apt/lists/*
```
- Use a non-root user (uid 1000) and ensure `/workspace` is writable.

3) devkit.yaml tips
```
workspace: ../../<your-repo-folder>
service: frontend
env:
  AWS_PROFILE: dev
env_files:
  - ../secrets/dev.env
hooks:
  warm: npm ci
  maintain: npm run build
```
- `service:` ensures CLI commands like `ssh-setup`, `repo-push-ssh`, `exec`, and `attach` target the right container.

4) Networking gotchas
- Your overlay service must join `dev-internal` to resolve `tinyproxy` and DNS sidecar names.
- Use `--profile dns` (default in our wrappers) so the agent DNS points to the allowlisted dnsmasq. The base kit wires this via the compose.dns.yml file.
- If Docker reports "pool overlaps" on the internal network, set a different CIDR and DNS IP before running:
  - `export DEVKIT_INTERNAL_SUBNET=172.31.10.0/24`
  - `export DEVKIT_DNS_IP=172.31.10.3`

5) SSH + Git gotchas
- Always run per-agent SSH setup at least once: `scripts/devkit -p <name> ssh-setup --index 1`
  - Writes keys and `known_hosts` into `/workspace/.devhome-agentN/.ssh`.
  - Writes SSH config for GitHub over 443 via the proxy: `Host github.com` + `ProxyCommand nc -X connect -x tinyproxy:8888 %h %p`.
 - Sets both global and repo-level `git config core.sshCommand 'ssh -F "$HOME/.ssh/config"'` and validates with `git pull --ff-only`.
- Uses an index-free HOME anchor: `/workspace/.devhome` → `.devhomes/<container-id>`. The SSH config uses `~/.ssh/...` so it never relies on a replica index.
- The CLI sets only the GLOBAL `core.sshCommand` and removes repo-local overrides that might point at legacy absolute paths.

Quoting pitfalls (important)
- ssh_config is not a shell; do not wrap paths in single quotes.
- IdentityFile and UserKnownHostsFile should be raw absolute paths or `~`-relative.
- When writing files via shell, always: `mkdir -p ~/.ssh && cat > ~/.ssh/config && chmod 600 ~/.ssh/config`.
- Flip origin to SSH (if currently HTTPS): `scripts/devkit -p <name> repo-config-ssh . --index 1`.
- If you see `Permission denied (publickey)`, confirm the host key you copied has access to the repo and that origin is SSH.

6) Git identity (required)
- The CLI refuses to open interactive windows unless it can determine both name and email via:
  - `DEVKIT_GIT_USER_NAME`, `DEVKIT_GIT_USER_EMAIL` (recommended), or
  - Host global config: `git config --global user.name` and `user.email`.
- The CLI sets `git config --global user.name/user.email` inside the container for tmux windows.

7) Tmux + layout tips
- Use a long-lived command (e.g., `sleep infinity`) so tmux can attach reliably.
- Layouts can target a specific `service` and `compose_project`; the CLI resolves containers by labels.
- Avoid `:` in tmux session names unless you intend `session:window` syntax.

8) Quick checklist
- [ ] `devkit.yaml` created with `workspace` and `service`.
- [ ] Compose override uses correct `build.context` path, mounts `${WORKSPACE_DIR}` into `/workspace`, and joins `dev-internal`.
- [ ] Dockerfile installs `netcat-openbsd` (for SSH proxy).
- [ ] Container stays up (keepalive command) so tmux/exec can attach.
- [ ] `ssh-setup` succeeds and `ssh-test` shows GitHub banner.
	- [ ] `repo-config-ssh` flips origin to SSH; `git pull` works inside the container.

Example references
- overlay-front-end-notes.md — a retrospective of issues and fixes when adding a Node frontend overlay.
- HTTPS ingress routing: ingress-routing-plan.md — describes the ingress block schema plus the CLI implementation details.

## Optional HTTPS Ingress Block

Overlays that need browser-friendly HTTPS hosts (e.g., `ouroboros.test`) can opt into the ingress service by adding an `ingress` block to `devkit.yaml`:

```
ingress:
  kind: caddy                 # currently only caddy is supported
  config: infra/Caddyfile     # optional; mount verbatim when provided
  routes:                     # alternatively, generate a config from host→service mappings
    - host: ouroboros.test
      service: frontend
      port: 4173
  certs:
    - path: infra/ouroboros.test.pem
    - path: infra/ouroboros.test-key.pem
  hosts:
    - ouroboros.test
    - webserver.ouroboros.test
  env:
    CADDY_DEBUG: "1"
```

Notes:
- When `config` is omitted, the CLI renders a simple Caddyfile from `routes` and mounts any listed `certs` into `/ingress/certs`. Without at least two cert entries the generated config falls back to `tls internal`.
- The ingress container publishes `443` to `127.0.0.1:${DEVKIT_INGRESS_PORT:-8443}`; override `DEVKIT_INGRESS_PORT` before `up` if you need a different binding.
- `hosts` documents the required hostnames and can be synced with `scripts/devkit -p <overlay> hosts apply --target host|agents|all`.
- See `ingress-routing-plan.md` for the long-form proposal, constraints, and Go implementation plan.
