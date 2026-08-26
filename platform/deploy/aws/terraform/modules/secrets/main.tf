###############################################################################
# secrets: NAMES + KMS encryption + tags only. VALUES are NEVER set by
# Terraform (no tfvars-committed secrets) — the operator populates each via
# `aws secretsmanager put-secret-value` exactly as scripted in RUNBOOK.md §5.
# Empty secrets are harmless: tasks simply fail until populated, and the
# deployment circuit breaker halts crash-loops.
#
# Custody notes:
# - ledger/joblease keys are Ed25519 PEMs. B6 graduation target is KMS-held
#   keys; the file-mount pattern here is the documented v0.2 interim
#   (init-container writes /keys/*.pem from these secret values).
# - db_dsns is ONE JSON secret with four DSN strings so ECS can inject a single
#   JSON field per task (arn:...:json-key:: syntax).
###############################################################################

locals {
  base = "/sauron/${var.environment}"
}

resource "aws_secretsmanager_secret" "this" {
  for_each = {
    db_dsns                    = "Full postgres:// DSNs per service. JSON keys: ingest_dsn, ctrl_dsn, fleet_dsn, conn_dsn."
    webhook_secret             = "GitHub App webhook HMAC secret; signs BOTH GitHub->ingest and ingest->ctrl hops (compose parity)."
    conn_webhook_secret        = "HMAC key for control-plane -> github-connector decision pushes (internal-protocols §4)."
    admin_token                = "Control-plane admin bearer; also web SAURON_ADMIN_TOKEN and connector rerun client token."
    conn_admin_token           = "github-connector installations/status bearer (SAURON_CONN_ADMIN_TOKEN); fails closed if unset."
    ledger_key                 = "Ed25519 PRIVATE key PEM signing ledger checkpoints. Control-plane ONLY (B6 custody). Generate: openssl genpkey -algorithm ed25519"
    joblease_key               = "Ed25519 PRIVATE key PEM signing job-lease JWTs (B2). Control-plane only; NEVER reuse ledger key."
    joblease_pub_key           = "Ed25519 PUBLIC key PEM (openssl pkey -pubout of joblease_key); runner-fleet verifies leases with it."
    github_app_private_key_pem = "GitHub App private key (.pem download) for live-mode check publishing. Required iff enable_connector_live_mode."
  }

  name                    = "${local.base}/${each.key}"
  description             = each.value
  recovery_window_in_days = 7 # soft-delete safety net for operator mistakes

  tags = { SecretClass = "sauron-runtime" }
}
