# CISync Oracle Always-Free RUNBOOK (Phase-1, ARM A1.Flex single host)

Ordered operator guide: zero → production on Ubuntu 24.04 ARM64,
4 OCPU / 24GB A1.Flex. Kit: `platform/deploy/oracle/`. Normative:
ENGINEERING_CHARTER · INVARIANTS **I-07** · ARCHITECTURE D2/D10 · SPEC §3.

## 1. OCI account + instance (the honest lottery)

1. Create an OCI account; pick a HOME REGION with A1 capacity — try
   `us-phoenix-1`, `eu-frankfurt-1`, `us-ashburn-1`. **"Out of capacity" is
   common for the always-free A1 shape**: retry different ADs, off-peak times,
   or recreate the instance request over days. This is a real lottery; budget
   patience. (Paid-tier accounts get priority; still free-tier eligible.)
2. Compute → Create Instance: shape **VM.Standard.A1.Flex**, **4 OCPU /
   24 GB**, image **Ubuntu 24.04 (aarch64) minimal**, boot volume 100 GB
   (free allotment max; ledger grows forever by design).
3. Networking: **reserve a public IP** (ephemeral IPs change on stop/start;
   reserved ones survive) and attach it. Note it.
4. Security List (or NSG): ingress **22/tcp (your IP only), 80/tcp, 443/tcp**
   — NOTHING else. Egress: allow all.
5. SSH in with your key (`ssh ubuntu@<IP>`); the default user is `ubuntu`.

## 2. Hardening basics

```bash
sudo apt-get update && sudo apt-get upgrade -y
sudo apt-get install -y ufw unattended-upgrades fail2ban
sudo ufw default deny incoming && sudo ufw default allow outgoing
sudo ufw allow OpenSSH && sudo ufw allow 80,443/tcp
sudo ufw enable
sudo dpkg-reconfigure -f noninteractive unattended-upgrades
sudo systemctl enable --now fail2ban        # optional but cheap
```
SSH is already key-only on Ubuntu cloud images (PasswordAuthentication no);
verify `/etc/ssh/sshd_config` and never disable this.

## 3. Docker Engine + compose plugin (apt official repo)

```bash
sudo apt-get install -y ca-certificates curl gnupg
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
  sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=arm64 signed-by=/etc/apt/keyrings/docker.gpg] \
  https://download.docker.com/linux/ubuntu noble stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list
sudo apt-get update && sudo apt-get install -y \
  docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker ubuntu   # re-login to take effect
```
WHY apt official repo, NOT `get.docker.com`: convenience scripts pipe a
remote shell script straight into root (no pinning, no rollback story) and
upstream explicitly labels them non-production; the apt repo gives you
signed, unattended-upgrades-compatible, versioned installs on arm64.

## 4. Deploy

```bash
cd ~ && git clone <your-fork> cisync && cd cisync/platform/deploy/oracle
cp .env.prod.example .env.prod && chmod 600 .env.prod   # fill EVERY value
#   - keygen commands are embedded as comments in .env.prod.example
#   - sudo apt-get install -y age ; age-keygen -o ~/age.key (chmod 600)
mkdir -p keys backups rclone && chmod 700 keys
openssl genpkey -algorithm ed25519 -out keys/ledger_ed25519.key
openssl genpkey -algorithm ed25519 -out keys/joblease_ed25519.key
openssl pkey -in keys/joblease_ed25519.key -pubout -out keys/joblease_ed25519.pub
docker compose --env-file .env.prod -f docker-compose.prod.yml up -d --build
```
First arm-native build takes ~5–10 min (Go ×4 + pnpm web). Migrations run at
boot inside each service binary (SPEC §3: schema-per-service ⇒ order-safe).

## 5. DNS + TLS

1. A record `<DOMAIN>` → reserved public IP. Caddy obtains/renews the
   Let's Encrypt cert automatically (http-01 on :80 + TLS-ALPN on :443).
2. Bare-IP fallback (no domain): edit Caddyfile per the template's trailing
   comment (`tls internal`). GitHub webhooks need valid TLS — bare-IP mode is
   dashboard-only; wire webhooks via the dev tunnel profile (RUNBOOK §2.3)
   until DNS exists.

## 6. Smoke checklist (= dev runbook posture)

```bash
curl -fsS https://$DOMAIN/ >/dev/null                 # dashboard loads
docker compose --env-file .env.prod -f docker-compose.prod.yml \
  exec caddy wget -qO- http://ingest:8080/healthz     # internal probe
curl -fsS https://$DOMAIN/api/cisync/candidates -H "Authorization: Bearer $CISYNC_CTRL_ADMIN_TOKEN"
curl -s -o /dev/null -w '%{http_code}' https://$DOMAIN/metrics   # expect 403
curl -s -o /dev/null -w '%{http_code}' https://$DOMAIN/internal/x # expect 403
```
Webhook fixture through the REAL URL once GitHub creds are wired: open a PR
on an installed repo → ledger gains `delivery.accepted → intent.declared →
candidate.submitted`; PR shows check **Agent Verification Gate** queued.
Storm-lite loadgen from INSIDE the network (never via published ports):
```bash
docker build -t cisync/loadgen -f tests/loadgen/Dockerfile tests/loadgen
docker run --rm --network cisync_cisync-net cisync/loadgen \
  -concurrency 50 -units 50 -repos 2 -dupes 1
```

## 7. BACKUPS — the feature (I-07: the ledger is irreplaceable)

```bash
sudo crontab -e    # MAILTO=you@example.com above the entry
0 3 * * * PROJECT_DIR=/home/ubuntu/cisync/platform/deploy/oracle \
  /home/ubuntu/cisync/platform/deploy/oracle/backup.sh >> /var/log/cisync-backup.log 2>&1
```
WEEKLY RESTORE DRILL (calendar it; an untested backup is Schrödinger's):
```bash
AGE_PRIVATE_KEY_FILE=~/age.key ./restore.sh backups/cisync-pg-<newest>.dump.gz.age
```
Compare printed scratch-db counts vs live (`ctrl.ledger` especially), then
follow the echoed manual-swap procedure ONLY when doing a real recovery.
Nightly chain verify runs in-process (`CISYNC_CTRL_VERIFY_INTERVAL=24h`);
also run `docker compose ... exec control-plane /app/control-plane verify`
after any restore.

**Recoverable-SPOF honesty statement:** CISync Phase-1 is a deliberate,
documented single-host SPOF. Losing the box costs at most 24h of deliveries
(the last backup window) plus ~15min of rebuild time (this runbook, section
by section, onto a fresh A1 instance). The ledger itself survives: it lives
in every daily encrypted archive replicated to R2. What we do NOT claim:
zero data loss, multi-AZ failover, or sub-minute RTO — those are v0.3+ with
managed PG, not promises bolted onto an always-free VM.

## 8. Upgrades

```bash
cd ~/cisync && git pull
cd platform/deploy/oracle
docker compose --env-file .env.prod -f docker-compose.prod.yml build
docker compose --env-file .env.prod -f docker-compose.prod.yml up -d
```
Migrations auto-run at each service's boot before serving (SPEC §3 AWS-row;
schema-per-service ⇒ any start order is safe — no one-off migration jobs).
Rollback: `git checkout <previous-tag>` + same commands (down-migrations
exist per ARCHITECTURE §2, but prefer forward-fix on append-only schemas).

## 9. Monitoring-lite

```bash
sudo crontab -e
*/5 * * * * /home/ubuntu/cisync/platform/deploy/oracle/healthcheck-loop.sh \
  >> /var/log/cisync-health.log 2>&1
```
The loop curls every service's `/healthz` through caddy (public) and via
`docker compose exec caddy wget …` (internal mesh: ingest/fleet/connector/
ctrl), appending timestamped OK/FAIL lines — grep FAIL to alert.
Disk guard ships inside backup.sh (exits non-zero under MIN_FREE_MB).
Optional nicer dashboards — uncomment when wanted (adds a container):
```yaml
# uptime-kuma (compose override file):
# services:
#   uptime-kuma:
#     image: louislam/uptime-kuma:latest
#     ports: ["127.0.0.1:3001:3001"]   # ssh -L 3001:localhost:3001 to view
#     volumes: [uptime-kuma:/app/data]
#     restart: unless-stopped
# volumes: { uptime-kuma: }
```

## Known limits (honest posture)

- Single host, single AZ; no automated failover (see §7 statement).
- sim provider pinned (B5: docker provider NOT-FOR-PRODUCTION until graduation).
- Single admin token; RBAC = v0.3. Ledger signing keys are file-based (D10:
  KMS/HSM graduation later).
- A1 free capacity is best-effort OCI inventory, not an SLA.
