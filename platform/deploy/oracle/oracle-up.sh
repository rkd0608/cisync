#!/usr/bin/env bash
# Boots (or upgrades) the CISync stack from platform/.env.prod secrets.
# WHY a wrapper: compose reads env-file relative paths consistently and
# migrations auto-run at boot per service, so upgrade == rebuild == this.
set -euo pipefail
"$HERE/box-sanitize.sh"
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
test -f .env.prod || { echo "missing .env.prod — copy from .env.prod.example"; exit 1; }
# Caddy needs a real file at ./Caddyfile (the template uses Caddy's own {$VAR}
# runtime substitution); without it Docker bind-mounts an auto-created dir.
test -f Caddyfile || cp Caddyfile.template Caddyfile
docker compose --env-file .env.prod -f docker-compose.prod.yml up -d --build
docker compose --env-file .env.prod -f docker-compose.prod.yml ps
