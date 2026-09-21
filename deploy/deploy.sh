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

COMPOSE=(docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env)

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
"${COMPOSE[@]}" run --rm --no-deps backend /app/migrate

echo "== start / restart"
"${COMPOSE[@]}" up -d --remove-orphans

echo "== bersihkan image lama"
docker image prune -f >/dev/null

echo
"${COMPOSE[@]}" ps
echo
echo "Cek log:   docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env logs -f backend"
