Retrospective — Git/SSH Reliability Across Overlays

Context
- Mixed orchestration (dev-all + frontend) must support `git pull` inside every container without manual steps.
- Failures appeared only in the frontend overlay: missing identities and known_hosts despite identical host keys.

Symptoms
- Frontend: `no such identity: '/workspace/.devhome/.ssh/id_ed25519'` and failure to add known_hosts.
- Dev‑all (dumb‑onion) worked consistently; ouroboros‑ide also worked.

Root Causes
- Index coupling: some paths still derived per‑agent homes from replica indices; windows were inconsistent.
- Seeding gaps: `exec` didn’t seed SSH/Git for the active window (relied on prior ssh‑setup/layout seeding).
- Quoting bug in ssh_config generation: IdentityFile and UserKnownHostsFile were written with shell‑style single quotes. OpenSSH treats those as literal characters, so it looked for a file literally named `'/workspace/.devhome/.ssh/id_ed25519'`.
- Occasional DNS/profile mismatch in the frontend overlay (not consistently pinning dnsmasq).

Fixes (what we shipped)
- Index‑free HOME anchor:
  - codex overlays: `/workspace/.devhome` → `/workspace/.devhomes/<container-id>`.
  - dev‑all overlay: `/workspaces/dev/.devhome` → `/workspaces/dev/.devhomes/<container-id>`.
- Seed on every `exec` and `exec-cd`:
  - Ensure anchor + `~/.ssh` exist; copy host `id_ed25519` (+`.pub`) and/or `id_rsa`; copy `known_hosts` if present.
  - Write proxy‑aware ssh_config (unquoted absolute paths); set `git config --global core.sshCommand 'ssh -F ~/.ssh/config'` under the anchor HOME.
  - Export `HOME` to the anchor and run your command.
- layout‑apply hardening:
  - Proactively `down --remove-orphans` and remove `*_dev-internal` / `*_dev-egress` networks for target compose projects to avoid CIDR/IP drift.
  - Bring up overlays and seed SSH/Git for each instance.
- Frontend overlay networking:
  - Join `dev-internal`, declare `dns: [ ${DEVKIT_DNS_IP} ]`, and depend on `dns` and `tinyproxy`.
- Quoting fix:
  - Generate ssh_config with unquoted absolute paths for IdentityFile and UserKnownHostsFile.

Validation
- In‑container checks now show:
  - `/workspace/.devhome -> /workspace/.devhomes/<container-id>` (or `/workspaces/dev/.devhome` for dev‑all)
  - `~/.ssh/{id_ed25519,id_rsa,known_hosts,config}` present under the anchor.
  - `git config --global --get core.sshCommand` set under the anchor HOME.
  - `ssh -F ~/.ssh/config -T github.com -o BatchMode=yes` returns a banner; `git pull --ff-only` works.

Lessons & How To Avoid This Class of Bugs
- Config files aren’t shells: never rely on shell quoting in ssh_config; write raw paths.
- Seed at point‑of‑use: don’t assume prior setup; make `exec` and `exec-cd` idempotently create ~/.ssh, write keys/config, and set global Git under the active HOME.
- Avoid replica indices: use a container‑unique anchor HOME; resolve containers by label, not compose `--index`.
- Always `mkdir -p` before writes from `cat >`: pair every write with an explicit directory create and chmod.
- Test where it runs: validate from inside the container with `ssh -F ~/.ssh/config` and `git pull`.

Follow‑ups & Guard Rails
- Unit tests for config builders to ensure no quotes and correct fields.
- Small integration probe that runs `ssh -F ~/.ssh/config -G github.com` and greps for IdentityFile/UserKnownHostsFile.
- Keep config generation in Go (no heredocs); use `RunWithInput` to write content and explicit chmods.
- Keep window/exec commands small and explicit; prefer label-based `docker exec` for resilience.

