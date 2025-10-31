SHELL := /bin/bash

# Paths
KIT        := kit
OVERLAYS   := overlays
PROJECT    ?= codex
CLI        := $(KIT)/bin/devctl
# Defaults for run/health flows
REPO       ?= ouroboros-ide
N          ?= 4

# Compose file set with all profiles + overlay
COMPOSE_ARGS := -f $(KIT)/compose.yml -f $(KIT)/compose.hardened.yml -f $(KIT)/compose.dns.yml -f $(KIT)/compose.envoy.yml -f $(OVERLAYS)/$(PROJECT)/compose.override.yml

.PHONY: build-cli codex-fresh-open codex-verify codex-down codex-ci health run

build-cli:
	@echo "== Building Go CLI -> $(CLI) =="
	@$(MAKE) -C cli/devctl build

# Fresh open with all profiles, N agents, tmux disabled for non-interactive runs
# Usage: make codex-fresh-open N=1 [INSTALL_CODEX=false INSTALL_CLAUDE=false]
codex-fresh-open: build-cli
	@echo "== Fresh open for $(PROJECT) with all profiles (N=$(N)) =="
	@export DEVKIT_NO_TMUX=1; \
	  N=${N:-1}; \
	  $(CLI) -p $(PROJECT) fresh-open $$N

# Verify core behavior inside the dev-agent: proxy env, git, codex/claude, hardened rootfs
codex-verify:
	@echo "== Verifying dev-agent behavior (proxy env, git, codex/claude, hardened) =="
	@docker compose $(COMPOSE_ARGS) exec -T dev-agent bash -lc "env | grep -E '^HTTPS?_PROXY=|^NO_PROXY=' || true"
	@docker compose $(COMPOSE_ARGS) exec -T dev-agent git --version
	@docker compose $(COMPOSE_ARGS) exec -T dev-agent bash -lc "timeout 10s codex --version || timeout 10s codex exec 'ok' || true"
	@docker compose $(COMPOSE_ARGS) exec -T dev-agent bash -lc "timeout 10s claude --version || timeout 10s claude --help || true"
	@docker compose $(COMPOSE_ARGS) exec -T dev-agent bash -lc "touch /should_fail && echo wrote || echo denied"

# Bring down everything (all profiles)
codex-down:
	@echo "== Bringing down $(PROJECT) (all profiles) =="
	@docker compose $(COMPOSE_ARGS) down || true
	@docker rm -f devkit_envoy devkit_envoy_sni devkit_dns devkit_tinyproxy >/dev/null 2>&1 || true
	@docker network rm devkit_dev-internal devkit_dev-egress >/dev/null 2>&1 || true

# End-to-end: build, fresh-open, verify, and leave up
codex-ci: build-cli codex-fresh-open codex-verify
	@echo "== Codex E2E completed. Use 'make codex-down' to clean up. =="

# Unified health check: verifies ssh + codex + worktrees for both overlays
health: build-cli
	@echo "== Health: codex overlay =="
	@$(CLI) -p codex verify
	@echo "== Health: dev-all overlay =="
	@$(CLI) -p dev-all verify

.PHONY: test-runtime
test-runtime:
	@echo "== Running runtime integration tests =="
	@cd cli/devctl && DEVKIT_RUNTIME_TESTS=1 go test -timeout 20m ./integration/runtime -count=1

# Idempotent run: ensure worktrees and bring up N agents with tmux windows
run: build-cli
	@echo "== Run: $(REPO) with N=$(N) agents (dev-all overlay) =="
	@$(CLI) -p dev-all run $(REPO) $(N)
