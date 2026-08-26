#!/usr/bin/env bash
# CISync Phase-1 restore drill — RUNBOOK-oracle §7 (WEEKLY, rehearsed).
# Validates an encrypted archive end-to-end WITHOUT touching the live stack:
#   decrypt -> gzip integrity -> pg_restore --list -> FULL restore into a
#   throwaway postgres container -> key-table row counts printed.
#
# The REAL swap into place is deliberately MANUAL (destructive; see the
# procedure echoed at the end) — automation must never be one typo away
# from dropping prod pgdata.
#
# Usage:  AGE_PRIVATE_KEY_FILE=./age.key ./restore.sh backups/cisync-pg-*.dump.gz.age
set -euo pipefail

ARCHIVE="${1:?usage: restore.sh <archive.dump.gz.age>}"
AGE_PRIVATE_KEY_FILE="${AGE_PRIVATE_KEY_FILE:?export AGE_PRIVATE_KEY_FILE=/path/to/age.key}"
RESTORE_IMAGE="${RESTORE_IMAGE:-postgres:16-alpine}"
DRILL_CONTAINER="cisync-restore-drill"

command -v age >/dev/null || { echo "FATAL: age not installed" >&2; exit 2; }
[ -f "$ARCHIVE" ] || { echo "FATAL: archive not found: $ARCHIVE" >&2; exit 2; }
[ -f "$AGE_PRIVATE_KEY_FILE" ] || { echo "FATAL: identity file missing: $AGE_PRIVATE_KEY_FILE" >&2; exit 2; }

scratch=$(mktemp -d /tmp/cisync-restore.XXXXXX)
trap 'docker rm -f "$DRILL_CONTAINER" >/dev/null 2>&1 || true; rm -rf "$scratch"' EXIT

echo "[drill] decrypting + integrity-checking $(basename "$ARCHIVE")"
if ! age -d -i "$AGE_PRIVATE_KEY_FILE" < "$ARCHIVE" > "$scratch/dump.gz" 2>"$scratch/age.err"; then
	echo "FATAL: decryption failed — wrong key or corrupt archive: $(cat "$scratch/age.err")" >&2
	exit 1
fi
gzip -t "$scratch/dump.gz" || { echo "FATAL: gzip integrity check failed" >&2; exit 1; }
gunzip -c "$scratch/dump.gz" | head -c5 | grep -q '^PGDMP' \
	|| { echo "FATAL: not a pg_dump -Fc stream" >&2; exit 1; }
echo "[drill] envelope OK ($(du -h "$scratch/dump.gz" | cut -f1) decompressed)"

echo "[drill] booting throwaway postgres ($RESTORE_IMAGE)"
docker run -d --rm --name "$DRILL_CONTAINER" \
	-e POSTGRES_USER=cisync -e POSTGRES_PASSWORD=drill-only \
	-e POSTGRES_DB=cisync "$RESTORE_IMAGE" >/dev/null
for i in $(seq 1 30); do
	docker exec "$DRILL_CONTAINER" pg_isready -U cisync >/dev/null 2>&1 && break
	[ "$i" = 30 ] && { echo "FATAL: throwaway postgres never became ready" >&2; exit 5; }
	sleep 1
done

echo "[drill] pg_restore --list (archive readable by this PG version)"
docker exec -i "$DRILL_CONTAINER" pg_restore --list < "$scratch/dump.gz" > "$scratch/toc.txt" \
	|| { echo "FATAL: pg_restore --list FAILED — archive unusable" >&2; exit 1; }
echo "[drill] TOC entries: $(grep -c '^[0-9]*;' "$scratch/toc.txt")"

echo "[drill] full restore into scratch db (this is the real test)"
docker exec -i "$DRILL_CONTAINER" pg_restore -U cisync -d cisync --no-owner --exit-on-error \
	< "$scratch/dump.gz" > "$scratch/restore.log" 2>&1 \
	|| { echo "FATAL: pg_restore FAILED:" >&2; tail -20 "$scratch/restore.log" >&2; exit 1; }

echo "[drill] row counts in restored scratch db:"
for tbl in ctrl.ledger ctrl.ledger_checkpoints ingest.deliveries ctrl.decisions fleet.execution_jobs ghconn.check_reports; do
	n=$(docker exec "$DRILL_CONTAINER" psql -U cisync -d cisync -tAc \
		"SELECT count(*) FROM $tbl" 2>/dev/null || echo "table-missing")
	printf '  %-28s %s\n' "$tbl" "$n"
done
live=$(docker compose -f "$(dirname "$0")/docker-compose.prod.yml" \
	--env-file "$(dirname "$0")/.env.prod" ps -q postgres 2>/dev/null || true)
if [ -n "$live" ]; then
	live_n=$(docker exec "$live" psql -U cisync -d cisync -tAc "SELECT count(*) FROM ctrl.ledger" 2>/dev/null || echo "?")
	echo "[drill] live ctrl.ledger count for comparison: $live_n"
fi

cat <<'SWAP'

[drill] SCRATCH RESTORE PASSED. Manual swap into place (REHEARSE BEFORE YOU NEED IT):
  1. docker compose -f docker-compose.prod.yml --env-file .env.prod stop
  2. docker volume inspect cisync_pgdata   # note mountpoint
  3. mv <pgdata-dir> <pgdata-dir>.quarantine-$(date -u +%Y%m%dT%H%M%SZ)
     # keep the quarantine dir until the new volume is VERIFIED serving traffic
  4. age -d -i $AGE_PRIVATE_KEY_FILE < ARCHIVE | gunzip > /tmp/dump.fc
  5. docker volume create cisync_pgdata && start ONLY postgres:
     docker compose -f docker-compose.prod.yml --env-file .env.prod up -d postgres
  6. docker exec -i $(docker compose ... ps -q postgres) \
       pg_restore -U cisync -d cisync --no-owner --clean --if-exists < /tmp/dump.fc
  7. docker compose -f docker-compose.prod.yml --env-file .env.prod up -d
  8. Re-run this drill's count comparison against the live stack; verify
     chain: docker compose exec control-plane /app/control-plane verify
SWAP
echo "[drill] OK"
