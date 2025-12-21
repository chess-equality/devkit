# HTTPS Ingress Plan (Caddy/Envoy Option)

Context: ouroboros developers already rely on a local HTTPS reverse proxy (Caddy + mkcert certs with hosts such as `ouroboros.test` and `webserver.ouroboros.test`) so their browser sessions pass CORS checks against the Scala/Vite stack. Inside the devkit, every overlay currently runs only on the internal Docker network with no published ports, so reproducing that ergonomics requires an ingress story that still respects the dual-network guarantees.

## Requirements
- Keep the base kit repo-agnostic; overlays that do not need HTTPS routing should not have to carry extra services.
- Preserve the existing security posture: dev-agent containers stay on `dev-internal`, no arbitrary host ports are exposed, and HTTPS termination happens in a controlled service.
- Mirror current developer workflow: custom hostnames backed by hosts-file entries plus mkcert-managed certificates.
- Allow per-repo customization (full Caddyfile) without forcing every project to adopt the same config, while also supporting a structured “just give me host→service routes” helper for repos without bespoke configs.

## Options Considered
1. **Direct port bindings.** Expose the Vite/backend ports from `dev-agent` straight to `127.0.0.1`. Pros: minimal change, works with host-run Caddy. Cons: undermines the “no host networking” contract, requires each developer to keep running Caddy manually, and leaks implementation details (ports) into the host.
2. **Host-level Caddy only.** Document that developers should keep running `caddy run` on the host, pointing at container ports via `localhost`. Better than raw ports but still forces manual orchestration per repo and complicates tmux/exec flows.
3. **Embedded ingress container (Caddy or Envoy).** Add an optional service (dual-homed like proxies) that terminates HTTPS using repo-provided certs and proxies traffic to the internal services. Developers still edit `/etc/hosts` to map `*.test` to `127.0.0.1`, but once traffic reaches the host it flows through the ingress container, keeping the rest of the overlay untouched.

We’re proposing option 3: introduce an explicit, opt-in ingress capability that the CLI wires only when requested.

## Proposed Interface
Add an `ingress` block to overlay `devkit.yaml` files:

```yaml
ingress:
  kind: caddy            # or envoy (future)
  config: infra/Caddyfile  # optional (mount verbatim if provided)
  certs:
    - path: infra/ouroboros.test.pem
    - path: infra/ouroboros.test-key.pem
  hosts:
    - ouroboros.test
    - webserver.ouroboros.test
  routes:
    - host: ouroboros.test
      service: frontend
      port: 4173
```

Rules:
- When `config` is supplied, the CLI mounts that file (plus any listed cert/key paths) into a shared ingress image and launches Caddy with it.
- When only `routes` are provided, the CLI renders a minimal Caddy config from a template that proxies each host to the requested overlay service/port and optionally mounts mkcert certs (either repo-committed or provided via env variables).
- Missing `ingress` means noop, so existing overlays remain unchanged.
- Hosts entries are documented so developers continue to add the appropriate `127.0.0.1 <host>` lines locally, matching the current mkcert flow.

Future work can add `kind: envoy` with a structured routes file if teams prefer an Envoy-based TLS proxy.

## Next Steps
1. Document the `ingress` schema in `devkit/kit/docs/new-overlay-guide.md` once the code path lands.
2. Teach `devkit/cli/devctl` to read the block, validate referenced files, and render/launch the ingress service automatically.
3. Update at least one overlay (ouroboros) to opt in, mount its repo-owned `infra/Caddyfile`, and copy the mkcert certs into the container so the Vite dev server and Scala backend are reachable via `https://ouroboros.test`.
4. Add test coverage (likely a dry-run inspect) to ensure the CLI refuses to start when required cert files are missing or routes reference unknown services.
