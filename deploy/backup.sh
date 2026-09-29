#!/bin/sh
# Operational backup of Postgres and the MinIO volume (infrastructure backup,
# independent of the users' E2EE history backup). Run from cron, e.g.
#   15 3 * * * /opt/kantra/deploy/backup.sh /var/backups/kantra
# then ship the directory off-site (restic/rsync).
set -eu
OUT=${1:-./backups}
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
COMPOSE="docker compose -f $(dirname "$0")/docker-compose.prod.yml --env-file $(dirname "$0")/.env"
mkdir -p "$OUT"

$COMPOSE exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -Fc "$POSTGRES_DB"' > "$OUT/postgres-$STAMP.dump"

# Consistent copy of the bucket data: tar the volume through a helper container.
docker run --rm -v kantra_minio_data:/data:ro -v "$(cd "$OUT" && pwd)":/out alpine \
  tar -C /data -czf "/out/minio-$STAMP.tar.gz" .

find "$OUT" -name 'postgres-*.dump' -mtime +14 -delete
find "$OUT" -name 'minio-*.tar.gz' -mtime +14 -delete
echo "backup written to $OUT ($STAMP)"
