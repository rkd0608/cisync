#!/usr/bin/env bash
# Sauron Phase-1 backup — Oracle Always-Free host (RUNBOOK-oracle §7).
# Cron entry (daily 03:00): see RUNBOOK-oracle §7. Exits non-zero on any
# failure so cron MAILTO delivers the alert; silence means success.
#
# Pipeline: pg_dump -Fc -> gzip -> age-encrypt -> ./backups/<ts>.dump.gz.age
#
# WHY age and NOT `openssl enc`: we pick ONE and this is it. age gives
# authenticated encryption (X25519 + ChaCha20-Poly1305) by default with a
# one-line interface; `openssl enc -aes-256-cbc` has NO MAC (bit-flipping
# attacks on encrypted-at-rest archives are realistic) and bolting HMAC on
# top is exactly the kind of hand-rolled crypto plumbing that rots. Cost:
# one tiny package — install once with `sudo apt-get install -y age`
# (Ubuntu 24.04 universe). No other host dependency is introduced.
#
# WHY gzip after pg_dump -Fc: Fc blocks are already compressed; the outer
# gzip normalizes the envelope and still shaves ~10-20% off Fc padding at
# trivial CPU cost on a 4-OCPU box. Belt-and-braces, not load-bearing.
#
# Remote copy: Cloudflare R2 via rclone, run as a CONTAINER (rclone/rclone)
# so the host stays clean — deploy-time tooling only, per charter §4.
#
# ── R2_SETUP.md (inline) ─────────────────────────────────────────────────
# 1. dash.cloudflare.com -> R2 -> Create bucket `sauron-backups` (default
#    location, NOT public). Free tier: 10GB storage/month, zero egress fees
#    — comfortably above our 14-retained daily dumps (~50MB each today).
# 2. R2 -> Manage API tokens -> Create API token: permissions
#    Object Read & Write, scope ONLY the sauron-backups bucket. Note the
#    Access Key ID / Secret Access Key + Account ID shown ONCE.
# 3. Create ./rclone/rclone.conf next to this script:
#      [sauron-r2]
#      type = s3
#      provider = Cloudflare
#      access_key_id = <ACCESS_KEY_ID>
#      secret_access_key = <SECRET_ACCESS_KEY>
#      endpoint = https://<ACCOUNT_ID>.r2.cloudflarestorage.com
#    chmod 600. Set RCLONE_REMOTE=sauron-r2:sauron-backups in .env.prod.
# ────────────────────────────────────────────────────────────────────────
set -euo pipefail
umask 077

PROJECT_DIR="${PROJECT_DIR:-$(cd "$(dirname "$0")" && pwd)}"
ENV_FILE="${ENV_FILE:-$PROJECT_DIR/.env.prod}"
BACKUP_DIR="${BACKUP_DIR:-$PROJECT_DIR/backups}"
RETAIN="${RETAIN:-14}"
MIN_ARCHIVE_BYTES="${MIN_ARCHIVE_BYTES:-1024}"
MIN_FREE_MB="${MIN_FREE_MB:-2048}"
RCLONE_IMAGE="${RCLONE_IMAGE:-rclone/rclone:latest}"

# Cron has no shell env: pull AGE_PUBLIC_KEY / RCLONE_REMOTE from .env.prod.
if [ -z "${AGE_PUBLIC_KEY:-}" ] && [ -f "$ENV_FILE" ]; then
	AGE_PUBLIC_KEY="$(grep -E '^AGE_PUBLIC_KEY=' "$ENV_FILE" | tail -1 | cut -d= -f2-)"
fi
if [ -z "${RCLONE_REMOTE:-}" ] && [ -f "$ENV_FILE" ]; then
	RCLONE_REMOTE="$(grep -E '^RCLONE_REMOTE=' "$ENV_FILE" | tail -1 | cut -d= -f2-)"
fi
[ -n "${AGE_PUBLIC_KEY:-}" ] || { echo "FATAL: AGE_PUBLIC_KEY unset (set in env or $ENV_FILE)" >&2; exit 2; }
command -v age >/dev/null || { echo "FATAL: age not installed (apt-get install age)" >&2; exit 2; }

mkdir -p "$BACKUP_DIR"

# Disk-space guard: refuse to fill the disk with our own backups.
free_mb=$(df -Pm "$BACKUP_DIR" | awk 'NR==2 {print $4}')
if [ "$free_mb" -lt "$MIN_FREE_MB" ]; then
	echo "FATAL: only ${free_mb}MB free under $BACKUP_DIR (< ${MIN_FREE_MB}MB); prune or expand." >&2
	exit 9
fi

pg_container=$(docker compose --env-file "$ENV_FILE" -f "$PROJECT_DIR/docker-compose.prod.yml" ps -q postgres)
[ -n "$pg_container" ] || { echo "FATAL: postgres container not found (stack up?)" >&2; exit 3; }

stamp=$(date -u +%Y%m%dT%H%M%SZ)
target="$BACKUP_DIR/sauron-pg-$stamp.dump.gz.age"
tmp="$target.partial"

echo "[backup] dumping $pg_container -> $target"
docker exec "$pg_container" pg_dump -U sauron -d sauron -Fc \
	| gzip \
	| age -r "$AGE_PUBLIC_KEY" > "$tmp"
mv "$tmp" "$target"

# Verify newest archive non-empty AND decryptable to a pg_dump stream.
size=$(stat -c%s "$target" 2>/dev/null || echo 0)
if [ "$size" -lt "$MIN_ARCHIVE_BYTES" ]; then
	echo "FATAL: $target is ${size}B (< ${MIN_ARCHIVE_BYTES}B) — backup FAILED" >&2
	exit 1
fi
if ! age -d < "$target" 2>/dev/null | gunzip 2>/dev/null | head -c5 | grep -q '^PGDMP'; then
	echo "FATAL: $target does not decrypt to a pg_dump stream — backup FAILED" >&2
	exit 1
fi
echo "[backup] verified ($((size / 1024)) KiB, PGDMP magic ok)"

# Retention: keep newest RETAIN local archives.
ls -1t "$BACKUP_DIR"/sauron-pg-*.dump.gz.age 2>/dev/null | tail -n +$((RETAIN + 1)) | while read -r old; do
	rm -f -- "$old"
	echo "[backup] pruned $old"
done

# Off-site copy to R2 if configured (runs rclone as an ephemeral container).
if [ -n "${RCLONE_REMOTE:-}" ] && [ -f "$PROJECT_DIR/rclone/rclone.conf" ]; then
	docker run --rm \
		-v "$PROJECT_DIR/rclone/rclone.conf:/config/rclone/rclone.conf:ro" \
		-v "$BACKUP_DIR:/backups:ro" \
		"$RCLONE_IMAGE" \
		copy "/backups/$(basename "$target")" "$RCLONE_REMOTE/" \
		&& echo "[backup] copied to $RCLONE_REMOTE" \
		|| { echo "FATAL: rclone copy failed — archive is LOCAL-ONLY" >&2; exit 4; }
else
	echo "[backup] RCLONE_REMOTE unset or rclone.conf missing — local-only (NOT recommended)"
fi
echo "[backup] OK $target"
