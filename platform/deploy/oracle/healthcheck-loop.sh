#!/usr/bin/env bash
# CISync Phase-1 monitoring-lite probe (RUNBOOK-oracle §9).
# Cron every 5 min as root or ubuntu. Appends timestamped OK/FAIL lines;
# grep FAIL /var/log/cisync-health.log to alert.
set -u
PROJECT_DIR="${PROJECT_DIR:-$(cd "$(dirname "$0")" && pwd)}"
ENV_FILE="$PROJECT_DIR/.env.prod"
COMPOSE="docker compose --env-file $ENV_FILE -f $PROJECT_DIR/docker-compose.prod.yml"
DOMAIN=$(grep -E '^DOMAIN=' "$ENV_FILE" | tail -1 | cut -d= -f2-)
ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
fail=0

# Public surface through real TLS (caddy -> web).
if curl -fsS --max-time 10 "https://$DOMAIN/" > /dev/null 2>&1; then
	echo "$(ts) OK public web"
else
	echo "$(ts) FAIL public web"; fail=1
fi

# Internal mesh probes ride caddy's alpine busybox wget on cisync-net.
probe() { # probe <svc-host> <port> <path>
	if $COMPOSE exec -T caddy \
		wget -qO- --timeout=5 "http://$1:$2$3" > /dev/null 2>&1; then
		echo "$(ts) OK internal $1"
	else
		echo "$(ts) FAIL internal $1"; fail=1
	fi
}
probe ingest 8080 /healthz
probe control-plane 8081 /healthz
probe runner-fleet 8082 /healthz
probe github-connector 8083 /healthz

# Postgres liveness via pg_isready inside the db container itself.
pg=$($COMPOSE ps -q postgres)
if [ -n "$pg" ] && docker exec "$pg" pg_isready -U cisync -d cisync > /dev/null 2>&1; then
	echo "$(ts) OK postgres"
else
	echo "$(ts) FAIL postgres"; fail=1
fi

# Disk guard: warn loudly before backups start failing their own MIN_FREE_MB.
free_mb=$(df -Pm /var/lib/docker 2>/dev/null | awk 'NR==2 {print $4}')
[ -n "$free_mb" ] && [ "$free_mb" -lt 5120 ] && { echo "$(ts) FAIL disk ${free_mb}MB free (<5GB)"; fail=1; }

exit $fail
