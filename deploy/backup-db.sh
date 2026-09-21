#!/usr/bin/env bash
# Nightly dump of the self-hosted Supabase database, kept for 14 days.
#
#   /opt/salesan/app/deploy/backup-db.sh          # run once
#   crontab: 30 2 * * * /opt/salesan/app/deploy/backup-db.sh >> /var/log/salesan-backup.log 2>&1
#
# The dumps live on this machine only. If the VPS is lost they are lost with
# it; copy them somewhere else if that matters.
set -euo pipefail
DIR=/opt/salesan/backups
mkdir -p "$DIR" && chmod 700 "$DIR"
stamp=$(date +%Y%m%d-%H%M%S)
out="$DIR/salesan-$stamp.dump"
# Custom format: compressed, restorable table by table with pg_restore.
docker exec supabase-db pg_dump -U postgres -d postgres -F c \
  -n public -n auth -n storage > "$out"
find "$DIR" -name 'salesan-*.dump' -mtime +14 -delete
echo "$(date -Is) ok $(du -h "$out" | cut -f1) $out"
