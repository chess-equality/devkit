# Layout Apply Window Reuse Fix

## Summary
- Layouts that reuse the same container for multiple tmux windows now keep every window alive.
- Containers are resolved once per window, and mux commands call `docker exec` directly using the resolved name.
- Codex/SSH seeding is skipped when a container has already been prepared to avoid wiping credentials for earlier windows (e.g., `front-1` vs `front-2`).

## Operational Notes
- `scripts/devkit -p dev-all layout-apply --file devkit/kit/examples/orchestration-ouro8-doh1-front2-devall1.yaml` now produces the full window set (`ouro-1` … `ouro-8`, `doh-1`, `front-1`, `front-2`, `dev-all-1`).
- Direct `docker exec` lookups mean tmux commands are resilient to container index gaps and restarts.
- Reapplying a layout is idempotent with respect to Codex credentials because reseeding is skipped for already-initialized containers.

## Testing
```
scripts/devkit -p dev-all layout-apply --file devkit/kit/examples/orchestration-ouro8-doh1-front2-devall1.yaml
```
