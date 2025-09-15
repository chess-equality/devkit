Devkit Environment Helpers

Purpose: keep secrets out of git, make local dev easy, and provide simple validation and visibility.

Files loaded (highest precedence last):
- .env
- .env.shared
- .env.${ENV}
- .env.local
- .env.${ENV}.local

Commands
- load.sh: Load layered dotenv files into the current shell (source it).
- init.sh: Create .env.local from .env.example if present.
- validate.sh: Validate required variables are set (via env.required file).
- print.sh: Print effective env with values redacted.

Per‑project config
- env.required (optional): newline‑separated list of required variable names.
- .env.example (recommended): template of variables for contributors.

Examples
- source devkit/env/load.sh && ./devkit/env/validate.sh
- ./devkit/env/init.sh
- ./devkit/env/print.sh

