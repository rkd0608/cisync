# Sauron AWS Deployment Kit — Operator RUNBOOK (v0.2, ECS Fargate)

Ordered steps to stand up `sauron-<env>` from zero in one AWS account.
You run every AWS command; this kit never does. Companion: `ENV_MATRIX.md`
(every compose var -> prod source). Terraform entrypoint: `terraform/`.

## 1. Prerequisites (account-side)

| Tool | Version check |
|---|---|
| aws cli v2 | `aws --version` |
| terraform | `>=1.6` (`terraform version`) |
| docker | running daemon |
| openssl | any modern (ed25519 support) |

Account-side state you must create BEFORE step 3:

1. **Route53 domain** (or bring your own DNS; then leave `hosted_zone_id=""`).
2. **ACM cert ISSUED in your target region** for e.g. `sauron.example.com`:

   ```bash
   aws acm request-certificate --domain-name sauron.example.com \
     --validation-method DNS
   # add the CNAME it prints; wait until Status=ISSUED
   ```

3. OIDC role for CI (optional, only if using `.github/workflows/deploy.yml`):
   create an IAM OIDC provider for `token.actions.githubusercontent.com` plus a
   deploy role; set repo variables `AWS_REGION` + `AWS_DEPLOY_ROLE_ARN` and add
   required reviewers on the matching GitHub Environment (that IS the approval gate).
4. `cd platform/deploy/aws/terraform && cp terraform.tfvars.example terraform.tfvars`
   and fill region/cert/domain. NEVER put secret VALUES anywhere in tfvars.

## 2. First apply (no services yet)

```bash
cd platform/deploy/aws/terraform
terraform init
terraform plan -var="enable_services=false"   # review ~60 resources
terraform apply -var="enable_services=false"
```

Why services OFF: images aren't pushed and secrets are empty; ECS tasks would
crash-loop into the deployment circuit breaker. Everything else comes up:
VPC/2-AZ/NAT, ALB+listeners+TGs, ECR x5, RDS Postgres 16 (encrypted, private,
deletion-protected), Secrets Manager shells (empty values), CloudMap, log groups.

Record outputs: `terraform output` — you need `ecr_repository_urls`,
`secret_arns`, `rds_master_secret_arn`.

## 3. Build & push images

```bash
cd platform/deploy/aws            # script resolves account/registry itself
AWS_REGION=us-east-1 ./push-images.sh          # tag = current git short SHA
# or pin: ./push-images.sh --tag $(git rev-parse --short HEAD)
```

Contexts verified against real Dockerfiles: `services/{ingest,control-plane,
runner-fleet,github-connector}` (Go multi-stage, migrations/ embedded into the
image) and `apps/web` (pnpm/corepack standalone build — no monorepo imports,
no build args needed because the API proxy is runtime-env configured).

## 4. Populate secrets (EXACT commands)

Generate keys locally, ship values via CLI; nothing secret is ever stored in
git/state plaintext beyond Secrets Manager itself. PEM convention: PKCS8
(`-----BEGIN PRIVATE KEY-----`), which is what Go's `x509.ParsePKCS8PrivateKey`
path in `internal/store.LoadSigningKey` expects.

```bash
ENV=prod                                   # staging|prod — must match tfvars
S() { echo "sauron-$ENV/$1"; }

# --- Ed25519 signing keys (ledger != joblease; NEVER reuse across the two) ---
openssl genpkey -algorithm ed25519 -out /tmp/l.pem
openssl genpkey -algorithm ed25519 -out /tmp/j.pem
openssl pkey -in /tmp/j.pem -pubout -out /tmp/j.pub   # fleet verifies with this

aws secretsmanager put-secret-value --secret-id "$(S ledger_key)" \
  --secret-string "$(cat /tmp/l.pem)"
aws secretsmanager put-secret-value --secret-id "$(S joblease_key)" \
  --secret-string "$(cat /tmp/j.pem)"
aws secretsmanager put-secret-value --secret-id "$(S joblease_pub_key)" \
  --secret-string "$(cat /tmp/j.pub)"

rm /tmp/l.pem /tmp/j.pem /tmp/j.pub        # wipe local copies when done
```

Remaining string secrets:

```bash
aws secretsmanager put-secret-value --secret-id "$(S webhook_secret)" \
  --secret-string "<GitHub App webhook secret>"
aws secretsmanager put-secret-value --secret-id "$(S admin_token)" \
  --secret-string "$(openssl rand -hex 32)"        # ctrl+web+connector bearer
aws secretsmanager put-secret-value --secret-id "$(S conn_admin_token)" \
  --secret-string "$(openssl rand -hex 32)"
aws secretsmanager put-secret-value --secret-id "$(S conn_webhook_secret)" \
  --secret-string "$(openssl rand -hex 32)"
# live GitHub App mode ONLY (enable_connector_live_mode=true):
aws secretsmanager put-secret-value --secret-id "$(S github_app_private_key_pem)" \
  --secret-string "$(cat downloaded-app-private-key.pem)"
```

**DB DSNs** — build four DSNs from the RDS master secret + endpoint outputs,
then store as ONE JSON secret (tasks read their field via `json-key` ARN syntax):

```bash
EP=$(cd terraform && terraform output -raw rds_endpoint)
read U P < <(aws secretsmanager get-secret-value \
  --secret-id "$(cd terraform && terraform output -raw rds_master_secret_arn)" \
  --query SecretString --output text \
  | jq -r '[.username,.password] | @tsv')
for svc in ingest ctrl fleet conn; do :; done
aws secretsmanager put-secret-value --secret-id "$(S db_dsns)" --secret-string "$(jq -n \
  --arg b "postgres://$U:$P@$EP/sauron" '{
    ingest_dsn: ($b+"?pool_max_conns=20&statement_timeout=15000"),
    ctrl_dsn:   ($b+"?pool_max_conns=64&statement_timeout=15000"),
    fleet_dsn:  ($b+"?pool_max_conns=32&statement_timeout=15000"),
    conn_dsn:   $b }')"
```

### How FILE-based keys work on Fargate (flagged design note)

Control-plane consumes **file paths only** (`SAURON_CTRL_LEDGER_KEY_FILE`,
`SAURON_CTRL_JOBLEASE_KEY_FILE` -> `store.LoadSigningKey(path)` /
`joblease.NewSignerFromPEMFile`). The kit makes this clean without code changes:
a keystore init container reads PEMs from Secrets Manager (execution-role
injection) and writes them onto a shared emptyDir volume mounted at `/keys`;
the app container mounts `/keys` read-only after init SUCCESS. Same pattern for
fleet's pub key and connector's App PEM. Interim custody per THREAT_MODEL B6;
graduation target is KMS-held keys behind the existing task role seam.
Alternative env-var key material exists ONLY in fleet
(`SAURON_FLEET_JOBLEASE_PUB_B64`) and is deliberately unused here for uniformity.

## 5. Turn services on

```bash
cd terraform
terraform apply -var="enable_services=true" -var="image_tag=$(git rev-parse --short HEAD)"
```

## 6. Migrations — auto-run at boot (verified, no manual step)

All four Go services migrate BEFORE serving HTTP (`st.Migrate(ctx, dir)` in each
`cmd/*/main.go`; Dockerfiles embed `migrations/`). Schemas are per-service-owned
(ARCHITECTURE §2), so boot-order races between services cannot collide — no
`aws ecs run-task` one-offs required. ECS health grace period is 120 s to cover
migration time; the circuit breaker rolls back if a service still fails.

## 7. Smoke checklist

1. `curl https://<domain>/healthz` -> 200 via ALB->ingest target group.
2. Web UI loads at `https://<domain>/` (ALB default -> web TG :3000).
3. Internal mesh healthy:
   `aws ecs list-services --cluster sauron-$ENV-cluster ...` all RUNNING;
   CloudMap names resolve (`control-plane.sauron.local` etc.).
4. **Signed webhook fixture through the REAL URL** (compose-parity HMAC):

   ```bash
   BODY='{"type":"pr.opened",...}'; TS=$(date +%s)
   SIG=$(printf "%s.%s" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" | cut -d' ' -f2)
   curl -i https://<domain>/hooks/github \
     -H "Content-Type: application/json" -H "X-GitHub-Event: pull_request" \
     -H "X-Hub-Signature-256: sha256=$SIG" -H "X-GitHub-Delivery: smoke-$(date +%s)" \
     -d "$BODY"
   # expect 2xx; then confirm delivery row + forwarded event in logs/RDS
   ```

   (Exact signature scheme must match ingest's verifier — see
   `services/ingest/internal/api` webhook handler before scripting against prod.)
5. PR -> Check flow (only after GitHub App creds live-mode wired): open a PR on
   an installed repo, watch connector write the "Agent Verification Gate"
   check; dry-run mode instead logs would-be payloads to its log group.
6. Ledger chain verify ran at boot (`sauron_ledger_verify_result{status=ok}`
   in ctrl logs; SAURON_CTRL_VERIFY_INTERVAL=24h nightly thereafter).

## 8. Rollback

- Bad image: redeploy previous tag —
  `terraform apply -var="image_tag=<previous-sha>" -var="enable_services=true"`
  (deployment circuit breaker auto-rolls back failed deploys anyway).
- Bad task-def-only change: `aws ecs update-service --task-definition <family>:<prev-revision>`.
- Bad infra change: standard `terraform apply` of reverted tfvars/git state.
- Data safety: ledger is append-only; RDS deletion protection ON + final
  snapshot + PITR 14d. Never set `db_skip_final_snapshot=true` in prod.

## 9. Cost table (~us-east-1, per month, v0.2 sizing)

| Line item | Config | ~$/mo |
|---|---|---|
| NAT gateway **(BIGGEST FIXED ITEM)** | 1 shared (`single_nat_gateway=true`) | $33 + $0.045/GB egress |
| NAT alternative | per-AZ toggle doubles this line | $66+ |
| Fargate: ingest/web/github-connector | 0.5 vCPU / 1 GB x 3 | ~55 |
| Fargate: control-plane + runner-fleet | 1 vCPU / 2 GB x 2 | ~73 |
| RDS db.t4g.small single-AZ | +20 GB gp3 (+autoscale to 100) | ~15–18 |
| ALB | 1 + light LCU | ~17–25 |
| Secrets Manager | 9 secrets | ~3.6 |
| CloudWatch logs | 90d retention, low volume | ~2–5 |
| ECR storage | 5 repos, scan-on-push | ~1 |
| **Total** | | **~200–230 + data** |

Toggles that move the number: `single_nat_gateway` (biggest), `db_multi_az`
(+~13), per-service task sizes, container insights (off).

## 10. Teardown

`terraform destroy` will refuse while deletion protection/final-snapshot rules
hold — flip `db_deletion_protection=false` LAST and eyeball the final snapshot.
Secrets enter 7-day recovery window; ECR repos keep images unless explicitly
deleted (force_delete=false by design).
