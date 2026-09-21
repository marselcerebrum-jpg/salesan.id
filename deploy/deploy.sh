#!/usr/bin/env bash
# Deploy or update salesan.id on this server. Safe to run again and again.
#
#   bash deploy/deploy.sh            # pull latest code, migrate, rebuild, restart
#   bash deploy/deploy.sh --no-pull  # deploy what is checked out, without git pull
#
# Order matters: migrations run against the database BEFORE the new server
# starts, using the new image, so a server never runs ahead of its schema.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

COMPOSE=(docker compose -f deploy/docker-compose.prod.yml)
# A VPS whose nginx already owns 80/443: skip Caddy, publish on localhost only.
# The marker file is created once, by hand, on that server.
if [[ -f deploy/USE_NGINX ]]; then
  COMPOSE+=(-f deploy/docker-compose.nginx.yml)
fi
COMPOSE+=(--env-file deploy/.env)

for f in deploy/.env backend/.env; do
  if [[ ! -f "$f" ]]; then
    echo "Belum ada $f. Salin dari ${f}.example lalu isi." >&2
    exit 1
  fi
done

if [[ "${1:-}" != "--no-pull" ]] && [[ -d .git ]]; then
  echo "== git pull"
  git pull --ff-only
fi

echo "== build image"
"${COMPOSE[@]}" build --pull

echo "== migrasi database"
# --entrypoint, because the image's entrypoint is the server: without it the
# path is handed to /app/server as an argument and a second server starts.
"${COMPOSE[@]}" run --rm --no-deps --entrypoint /app/migrate backend

echo "== start / restart"
"${COMPOSE[@]}" up -d --remove-orphans

echo "== bersihkan image lama"
docker image prune -f >/dev/null

echo
"${COMPOSE[@]}" ps
echo
echo "Cek log:   ${COMPOSE[*]} logs -f backend"
