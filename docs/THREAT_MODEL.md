# Sauron Threat Model & Security Requirements (FROZEN v1)

> Full analysis: `docs/plans/SECURITY_TRUST_DRAFT.md` (informative). This file is the
> binding subset for builders.

## P0 threats (v1 blockers)

| ID | Threat | Countermeasure (binding) |
|----|--------|--------------------------|
| T1 | Secret exfiltration via malicious patch output/logs | Redaction middleware fail-closed BEFORE persistence/model use; pattern classes: PATs, bearer/JWT, AWS/GCP keys, connection strings, private key blocks |
| T2 | Quarantine gaming (agent suppresses own failing test via flake path) | v1: flake classes require corroboration; quarantine never silently drops required evidence — routed_action=escalate_human |
| T3 | Evidence forgery by compromised runner | Runners submit data; control-plane authors+signs facts (Ed25519); lease tokens asymmetric+fenced; one accepted record per jti (I-03/I-12) |
| T5 | Cross-tenant ID enumeration | Uniform 404; tenant predicate from token only; ULIDs non-sequential-enough + rate-limited reads |
| T6 | Webhook replay/forgery | HMAC-SHA256 verify, timestamp tolerance ±5 min, `(source, ext_delivery_id)` unique dedup, raw payload stored before processing |
| T7 | Cache poisoning across trust domains | Artifact/cache keys namespaced tenant/repo/trust-domain + full inputs_hash |

## Binding security requirements

B1. Ledger INSERT/SELECT-only at DB level (trigger raises on UPDATE/DELETE).
B2. Job-lease JWTs: Ed25519-signed, aud=fleet, exp ≤60 m, claims bind run_id +
    attempt + repo + tier + jti; fleet verifies before claim AND before complete.
B3. No secret ever logged; redaction at ingest edge + log hooks; scrubber fails closed.
B4. Tenant isolation: row predicates everywhere + uniform 404 (no 403 leaks).
B5. Sandbox contents = code + fixtures only; egress default-deny; docker provider
    runs `--network none --read-only` rootfs + tmpfs, resource-capped, NOT-FOR-PRODUCTION
    until graduation checklist (SECURITY_TRUST_DRAFT §6.3) passes.
B6. Checkpoint signing key custody: control-plane only (dev: file-mounted; prod: KMS).
B7. Security-audit-grade events (authn failures, signature failures, budget violations,
    tamper detections, quarantine actions) → dedicated audit log stream, retained ≥90 d.

## Dev posture statement

docker-compose local deployment with sim/docker providers is DEV/DEMO ONLY. Production
graduation requires: hardened isolation (gVisor/Firecracker or equivalent), egress
allowlists, KMS-held keys, PG backups w/ defined RPO, multi-replica HA, pen-test pass.
