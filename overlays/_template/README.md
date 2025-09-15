Overlay Template (_template)

How to use
- Copy the entire `_template/` folder to a new overlay name under `devkit/overlays/<your-overlay>/`.
- Edit the following:
  - `devkit.yaml`:
    - `workspace: ../../path-to-your-repo` → point to your repo path relative to `devkit/overlays/<your-overlay>/`.
    - `service: app` → your service name (optional).
  - `compose.override.yml`:
    - `context: ../overlays/_template` → change to `../overlays/<your-overlay>`.
    - Volume: `../../path-to-your-repo:/workspace:rw` → update to your repo.
    - Service name `app` → rename if you changed `service:` in `devkit.yaml`.
  - `Dockerfile`:
    - Adjust base image and tools for your stack; keep `git`, `openssh-client`, and `netcat-openbsd` to support SSH via proxy.

Bringing it up
- Build and run: `devkit/kit/scripts/devkit -p <your-overlay> up --profile dns --build -d`
- Exec a shell: `devkit/kit/scripts/devkit -p <your-overlay> exec 1 bash`

SSH/Git setup
- Per container: `devkit/kit/scripts/devkit -p <your-overlay> ssh-setup --index 1`
  - Ensures HOME anchor `/workspace/.devhome` → `.devhomes/<container-id>`.
  - Writes SSH config with `~/.ssh/...` and sets global `git config core.sshCommand 'ssh -F ~/.ssh/config'`.
  - Validates with `GIT_SSH_COMMAND="ssh -F ~/.ssh/config" git pull --ff-only`.

Notes
- The overlay joins the `dev-internal` network so `tinyproxy` resolves.
- The Dockerfile includes `netcat-openbsd` to satisfy `ProxyCommand nc ...` in SSH config.

