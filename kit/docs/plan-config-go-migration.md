Plan — Configs in Go and Heredoc Audit

Objective
- Eliminate brittle shell heredocs for config/content writes in our dev flows. Move to typed generators and safe writers (Go + RunWithInput) where possible.

Scope of audit (current repo)
- Devkit CLI (Go): already generates ssh_config via `internal/sshcfg` (fixed quoting). Continue expanding this pattern.
- Shell heredocs found (snapshot via ripgrep):
  - ouroboros-ide (and agentN copies):
    - scripts/configure_codex_credential_file.sh (multiple heredocs `EOEXPORTS`)
    - scripts/environment-setup/install-temurin-jdk.sh (writes `/etc/profile.d/java_proxy.sh`)
    - scripts/environment-setup/persist-java-path.sh (writes `/etc/profile.d/java_sbt.sh`)
    - ourocast/generate-irs-jwk.sh (node <<'EOF')
  - dumb-onion-hax: Codex_Environment_Setup.sh (writes `/etc/profile.d/java_sbt.sh`)
  - terraform: multiple user_data heredocs, SMTP test heredocs, credential heredocs (CREDS/EOEXPORTS), openssl here-docs
  - io/prod/...: terraform user_data heredocs

Categorization
1) Container runtime config (dynamic, per-window):
   - ssh_config: already in Go; continue with typed writer.
   - Other dotfiles (if introduced): generate via Go, write via `RunWithInput`.

2) Container image build-time config (static):
   - java_proxy.sh, java_sbt.sh profile snippets (in `ouroboros-ide` and dumb-onion-hax):
     - Move to committed template files under the overlay’s Docker build context or under devkit resources, COPY during image build.
     - Avoid writing via heredoc at runtime; prefer files in the image or mounted configs.

3) One-off provisioning (terraform user_data, openssl stdin blocks):
   - Terraform user_data heredocs are appropriate; keep but lint for quoting consistency.
   - Openssl here-docs for SMTP tests can stay; add a small wrapper script to pipe content via stdin explicitly instead of heredoc if desired.

Migration steps
- Phase 1 (Devkit runtime):
  - Centralize all runtime file writes in devkit CLI using Go `RunWithInput`. Patterns already used in `internal/ssh`.
  - Add helpers for common patterns: “write file with mkdir -p + chmod”, “set global git under HOME”, etc.
  - Replace any remaining shell heredocs in devkit scripts (none currently beyond docs/examples) with CLI subcommands.

- Phase 2 (Overlay image configs):
  - For `ouroboros-ide` and dumb-onion-hax environment setup scripts:
    - Extract embedded heredoc content into tracked files:
      - `docker/dev/files/java_proxy.sh`
      - `docker/dev/files/java_sbt.sh`
    - Update Dockerfiles (or setup scripts) to COPY these files into `/etc/profile.d/` with correct perms.
    - Remove heredocs from the shell scripts.

- Phase 3 (Terraform/infra):
  - Mark terraform heredocs as allowed; add a lint rule to ignore `*.tf` user_data blocks.
  - For shell heredocs under `terraform/scripts/`, replace heredocs with `cat > file <<EOF` only when writing local dev files; otherwise use `printf` or here-strings. Prefer small scripts over inline heredocs.

Tooling & guardrails
- Add a simple CI check (ripgrep) to flag heredocs outside allowed directories:
  - Allowed: `**/*.tf` user_data, `openssl s_client <<EOF` blocks (tests), and explicitly whitelisted files.
  - Blocked: heredocs in `ouroboros-ide/scripts/**`, `dumb-onion-hax/**`, and `devkit/**` (runtime path) — suggest migration.
- Unit tests for config builders (ssh_config present). Add tests for any new config builders.
- Tiny integration probe:
  - Inside container: `ssh -F ~/.ssh/config -G github.com | grep -E '^identityfile|^userknownhostsfile'`
  - Ensures paths are unquoted and present.

Timeline & ownership
- Week 1:
  - Land heredoc lint, document allowed exceptions.
  - Extract java profile files to tracked files; update Docker contexts and scripts.
  - Document the new patterns in `contrib-quoting-and-file-writes.md`.
- Week 2–3:
  - Audit remaining scripts for heredocs; migrate low-risk items to Go CLI where useful.
  - Add integration probe for ssh/gits.

Notes
- We intentionally keep terraform user_data heredocs; they’re the idiomatic way to embed multi-line cloud-init.
- For runtime config, Go code + explicit writes avoids quoting pitfalls and makes problems easier to test.

Status (implemented)
- Replaced heredocs in runtime setup scripts with tracked files:
  - Added `ouroboros-ide/docker/dev/files/java_proxy.sh` and `java_sbt.sh`; environment setup scripts now install these files instead of writing via heredocs.
  - Mirrored the same change in `agent{2,3,4,5}/ouroboros-ide/...` copies.
  - Updated `dumb-onion-hax/Codex_Environment_Setup.sh` to install `docker/dev/files/java_sbt.sh` instead of a heredoc block.
- Added heredoc lint (focused): `devkit/kit/scripts/lint-heredocs.sh` checks the above paths are heredoc‑free.
- Credentials now generated via Go: added `devctl aws-cred write` and updated `ouroboros-ide` and `terraform/scripts/configure_codex_credential_file.sh` to delegate (with safe fallback). Terraform user_data heredocs remain intact.
