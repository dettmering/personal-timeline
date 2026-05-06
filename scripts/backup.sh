#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/var/backups/personal-timeline}"
KEEP_DAYS="${KEEP_DAYS:-30}"
VOLUME="timeline-data"
DB_PATH="/data/timeline.db"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
DEST="$BACKUP_DIR/timeline_$TIMESTAMP.db.gz"

mkdir -p "$BACKUP_DIR"

# SQLite online backup via .backup command — safe while the DB is in use
docker run --rm \
  -v "${VOLUME}:/data" \
  -v "$BACKUP_DIR:/backup" \
  alpine/sqlite "$DB_PATH" ".backup /backup/tmp_backup.db"

gzip -9 -c "$BACKUP_DIR/tmp_backup.db" > "$DEST"
rm -f "$BACKUP_DIR/tmp_backup.db"

# Remove backups older than KEEP_DAYS
find "$BACKUP_DIR" -name 'timeline_*.db.gz' -mtime +"$KEEP_DAYS" -delete

echo "Backup written: $DEST ($(du -sh "$DEST" | cut -f1))"
