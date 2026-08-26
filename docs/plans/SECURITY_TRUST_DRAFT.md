# CISync — Security & Trust Draft (v1)

Status: DRAFT · Owner: Security & Trust Engineer · Scope: v1 vertical slice, aligned with `ARCHITECTURE_DRAFT.md` (services, event envelope, hash-chained ledger) and `EDGE_CASES_DRAFT.md` (EC-xxx references) and `SRE_SCALABILITY_DRAFT.md` (budget/fence mechanics).
References: OWASP CI/CD Security Cheat Sheet; SLSA v1.x build requirements/levels; GitHub artifact attestations & GitHub App permission model.

---

## 0. Verdict on working defaults

| Default | Verdict | Notes |
|---|---|---|
| GitHub App installation tokens | **Adopt** | Never PATs, never OAuth user tokens, never org-wide classic tokens. Permissions: Metadata/Contents/PR/Actions read; Checks+Statuses write. Installation tokens NEVER cross into runner-fleet or execution (§1, B2). Private-key JWT lives only in github-connector. |
| HMAC-SHA256 webhook validation | **Adopt + harden** | Add timestamp tolerance window (replay age bound, EC-004), rotation overlap accepting old+new secrets in-window only (EC-010), constant-time compare, per-source lockout on failure bursts (EC-003). |
| Postgres hash-chained ledger | **Adopt** | Chain + signed checkpoints (§5). Answer to ARCH §9 open question: checkpoint signing key = platform-held Ed25519, v1 offline-rooted dev key w/ rotation script, Phase 2 KMS/HSM custody (§9 Q1). Services get INSERT/SELECT-only DB grants — no UPDATE/DELETE on ledger, ever (§7). |
| Per-job signed job leases (JWT-style) | **Adopt, amended** | Asymmetric Ed25519 (not HMAC): runner-fleet verifies without possessing mint capability. Claims pinned below (§3.2); revocation via fence epoch, not denylists. |
| docker-compose v1, no microVMs | **Accept as DEV/DEMO ONLY** | Isolation-by-construction compensating controls (§6.1) + hard NOT-FOR-PRODUCTION bar. Graduation criteria are normative (§6.3). |

---

## 1. Trust boundaries

```text
 EXTERNAL DOMAINS                          SAURON TRUST DOMAIN (v1: one compose network)
 ─────────────────                         ──────────────────────────────────────────────
 github.com                                ┌──────────────────────────────────────────────┐
     │  B1                                 │                                              │
     ├─webhooks (HMAC-SHA256 sig,          │   ┌────────┐  B3 svc token  ┌─────────────┐  │
     │  delivery GUID, X-Hub-Sig) ────────►│   │ ingest │──────────────►│control-plane│  │
     │                                     │   │        │◄───sync /cmd──│             │  │
 coding agents                            │   └───┬────┘   (mTLS in P2) └──┬───┬───┬──┘
     │  B0                                │       │                        │   │   │
     └─API key (hashed at rest) ──────────►       │            B4 lease JWT│   │   │ B2 install.
                                              │   │            (Ed25519,   │   │   │ token (≤1h,
 humans/web ──B7 session/admin tok ─────────► │   │             ≤60m,      │   │   │ repo-scoped;
                                              │   │             fenced)    │   │   │ NEVER past B4)
     ┌────────────────────────────────────────┘   ▼                        ▼   │   ▼
     │                                      ┌─────────────┐   ledger INSERT  │  github-
     │                                      │ runner-fleet│◄─only, chain─────┤  connector
     │                                      └──────┬──────┘   checkpoints    │  (Checks
     │                                             ║                          │  write-back)
     │                                    ═════════╫══ B5 UNTRUSTED CODE ═══  │
     │                                             ║   BOUNDARY               │
     │                                   B5′ lease-derived  ┌──────────────┐  │
     │                                   execution context  │ sim provider │  │
     │                                   (NO secrets, NO    │ docker (v1)  │  │
     │                                   standing creds,    │ gVisor/FC(P2)│  │
     │                                   egress default-    └──────┬───────┘  │
     │                                   deny)                     │ B6       │
     │                                             ┌───────────────▼────────┐ │
     └──────────── re-fetch originals ─────────────│ PG ledger+projections  │ │
                    (GitHub is raw-anchor SoS)     │ artifact/log store     │ │
                                                   │ (digest-addressed)     │ │
                                                   └────────────────────────┴─┘
```

| Boundary | Crossings | Credential / mechanism |
|---|---|---|
| **B0** agent→ingest | intents, candidates, reads | Per-agent API key (opaque, argon2id-hashed at rest) → resolved to `{tenant, agent}` principal; Idempotency-Key required |
| **B1** GitHub→ingest | PR/push/check events | HMAC-SHA256 (`X-Hub-Signature-256`) + delivery-GUID dedupe + age window; unsigned = 401, persisted as security event, never processed |
| **B2** connector↔github.com | check/status write-back, diff reads (W2) | Short-lived installation token (≤1h, repo-scoped by installation). **Hard rule: installation tokens terminate at connector; never forwarded to fleet/exec** |
| **B3** ingest/control-plane/connector | commands, events | Network-scoped compose + static per-service tokens; mTLS at Phase 2. Inbound `/commands` accepts only service principals |
| **B4** control-plane→runner-fleet | dispatch, heartbeats, completion | Job lease JWT (§3.2): audience=`fleet`, fence epoch, exp ≤60m |
| **B5** fleet→sandbox | job payload, results | Lease-derived execution context (subset claims). This is the adversarial boundary — assume total compromise of everything right of it |
| **B6** sandbox/fleet→stores | logs (redacted), artifacts, results | Digest-addressed writes keyed to lease jti + fence epoch; stale-epoch writes rejected (EC-046) |
| **B7** humans/web | dashboard reads, admin | v1: single admin token (env-held); P2: OIDC/SSO sessions. Web app has NO direct DB access |

---

## 2. Threat model (STRIDE-lite)

Agent-as-adversary is the default posture: every patch, log line, test result, and API call is potentially hostile. "Residual risk" states what remains AFTER the countermeasure.

| # | Threat (STRIDE) | Boundary/component | Attack scenario | Countermeasure | Residual risk | Pri |
|---|---|---|---|---|---|---|
| T1 | Secret exfil via patch output (InfoDisc) | B5/B6: exec→logs→storage/models | Malicious test prints env/files/`~/.aws` into stdout; logs stored, later summarized to LLM | No secrets exist in exec env by construction (§4.1); emit-time redaction middleware BEFORE storage & model-use (§4.2, fail-closed); ledger stores log digests only; egress default-deny (network-isolated v1, allowlist proxy P2) | Covert channels (timing, steganographic encodings) evade pattern redaction; heuristic anomaly detection only | **P0** |
| T2 | Quarantine gaming (Deception) | Evidence evaluator | Agent engineers flake-like signals so a genuinely failing test is quarantined off the gate | EC-041: quarantine requires corroboration across independent runs/environments + auto-expiry + human-visible quality obligation; no single actor (agent or runner) can suppress a gate test; quarantine-rate anomaly alert per agent | Patient multi-candidate farming of flake signals over weeks | **P0** |
| T3 | Evidence forgery by compromised runner (Spoof/Tamper) | B6 acceptance path | Worker fabricates `verdict=pass` without running anything, or replays a stale pass | Results accepted only from current fence-epoch holder; control-plane (not runner) evaluates + SIGNS evidence (§5); lease produces ≤1 accepted record (EC-035); SHA/digest binding at acceptance (EC-036); malformed/tampered → suspend runner (EC-038/043); simulator determinism cross-check | Full docker-host compromise can forge plausible results — outside v1 model; microVM attestation closes in P2 | **P0** |
| T4 | Budget-exhaustion DoS by one agent (DoS) | Admission/scheduler | One agent floods intents/candidates, drains compute, starves other tenants | Per-agent + per-tenant token buckets at ingest; intent concurrency caps; duplicate clustering collapses floods; atomic budget reservation/refund (SRE §3.3); machine-readable `budget_exhausted` feedback; per-tenant kill switch | Slow-drip inside legitimate quotas; distributed swarm across many onboarded identities (onboarding friction + anomaly alerts are the control) | P1 |
| T5 | Cross-tenant ID enumeration (InfoDisc) | All read APIs | Guessed IDs reveal valid tenants via 404-vs-403/timing diffs | Opaque ULIDs only (no sequential externals); uniform indistinguishable 404 for other-tenant IDs incl. timing shape (EC-050); tenant predicate mandatory in every query + RLS backstop (§3.4) | Traffic analysis / timing side channels | **P0** |
| T6 | Webhook replay/forgery (Spoof) | B1 ingest | Forged PR event injects phantom candidate; captured old delivery replayed to re-trigger work | HMAC constant-time verify; delivery-GUID dedupe (unique constraint, race-safe, EC-001/011); age-window rejection (EC-004); rotation overlap (EC-010); fail-closed tenant resolution (EC-008); failure-rate lockout (EC-003) | Webhook secret compromise → forged events until rotated; treat as P0 incident w/ rotation runbook | **P0** |
| T7 | Cache/artifact poisoning (Tamper/EoP) | Artifact store, evidence reuse | Hostile candidate plants an artifact/test-result reused as evidence by another tenant/repo/branch, or a fork-PR taints mainline cache | Keys namespaced `(tenant, repo, trust_domain, branch_class)`; full inputs-hash = f(base_sha, head_sha, lockfiles, flags, builder image digest, policy_version) — any change ⇒ miss (EC-034, invariant #2); cross-namespace fetch denied even for byte-identical content (EC-054); cluster-member reuse only same trust-domain + identical inputs-hash; digest verified on every fetch | Within-tenant cross-branch poisoning if an input is omitted from the key — mitigated by property tests; key-completeness review is a standing gate | **P0** |
| T8 | Audit rewrite by compromised worker (Repudiation) | Ledger | Runner/compromised service edits history to hide a wrong decision | Workers hold INSERT/SELECT-only DB grants (no UPDATE/DELETE on ledger); audit facts written only by control-plane; SHA-256 chain + nightly full-chain recompute + signed checkpoints; mismatch ⇒ fail-closed read-only mode + page (EC-056) | DBA-level or write-path-key compromise — detected (checkpoints), not prevented; external notarization deferred | P1 |
| T9 | Secrets-to-model leakage (InfoDisc) | Dossier/log summarization | Raw log/env dump shipped to external LLM vendor during failure diagnosis | Hard precondition: model calls consume ONLY redacted views (allowlisted fields, already-scrubbed text); redaction precedes storage too, so no unredacted copy exists to leak; vendor DPA required (§9 Q7) | Semantic leakage of proprietary code content itself (accepted; disclosed in Trust Center) | P1 |
| T10 | Repair path escape (EoP) | Repair envelope | Repair agent edits outside `allowed_paths`, e.g. weakens the failing test instead of the code (EC-020) | Server-side diff-path validation at submission + post-check scan; violation terminates envelope deterministically, raises incident event; contract enforced server-side, never by agent honor system | Semantically-out-of-scope edits WITHIN allowed paths — bounded by required revalidation evidence set | P1 |
| T11 | Credential over-scope by runner (EoP) | Lease issuance | Runner requests access beyond declared action/repo/env/TTL (EC-049, invariant #4) | Issuance denies beyond lease claims; scope re-checked at each resource touch; attempt audited; blast radius of any runner compromise = one job | Bugs in claim→resource mapping; covered by dedicated invariant suite | P1 |
| T12 | Ledger/checkpoint key compromise (Tamper/Repudiation) | §5 signing | Attacker holding checkpoint/evidence keys mints consistent-looking forged history | Key custody ladder (§5.3); rotation procedure; chain-verifier alarm on ANY mismatch; v1 keys never present in exec env (B5 rule) | Full control-plane compromise — out of threat model, detectability is the deliverable | P2 |

---

## 3. Identity & authorization model

### 3.1 Actors and tokens

| Actor | Token | Carries (claims) | Minted by | Verified by | TTL |
|---|---|---|---|---|---|
| Human (admin, v1) | Static admin token | `role=admin, tenant=<self>` | Install bootstrap | ingest + web backend | Rotated manually; P2 replaces with OIDC session (15m access / 8h refresh) |
| Agent | API key → principal | `tenant_id, agent_id, scopes[intent,candidate,read]`, key version | control-plane at agent onboarding | ingest (hash lookup) → propagated internally as verified principal | Long-lived credential; revocable instantly; hashed (argon2id) at rest |
| Platform service | Service token | `svc=ingest\|control\|fleet\|conn` | Compose env / secret store | Peer middleware | Static v1; mTLS certs P2 |
| GitHub | Installation token | Repo-scoped permissions per app registration | GitHub (via App private-key JWT held only by connector) | github.com | ≤60 min; never stored |
| Fleet worker | **Job lease JWT** (see 3.2) | Pinned claims below | control-plane at dispatch | runner-fleet APIs + evidence/artifact upload endpoints (public-key verify) | min(est_runtime×2, 60m); renewable via authenticated heartbeat |
| Sandbox process | Derived exec context | Subset of lease claims: `jti, tenant, repo, head_sha, action, egress_class` — **no signing/mint capability, no GitHub token, no DB creds** | fleet (in-memory injection) | Provider adapter | Lifetime of container |

### 3.2 Job lease token (normative claims)

```json
{ "iss":"sauron:control-plane", "aud":"sauron:fleet", "sub":"job_<ulid>",
  "jti":"<ulid>", "fence":17,
  "tenant_id":"org_..", "repo_id":"..", "base_sha":"..", "head_sha":"..",
  "env_class":"private|public_fork|release", "action":"tier1_unit|tier2_contract|..",
  "budget_ref":"intent_..", "exp":1755912000 }
```

Rules: (a) `fence` monotonic per job — stale fences rejected everywhere (EC-046/049); (b) one `jti` ⇔ at most one accepted evidence record (EC-035, invariant #3); (c) expiry ⇒ kill+fence, ghost completions rejected (EC-042); (d) private key lives only in control-plane; fleet gets public key. Challenge answered: HMAC would give fleet a mint-capable secret — rejected.

### 3.3 Permission matrix (v1 endpoints × principal)

| Action | anon | agent | fleet-worker | connector | admin |
|---|---|---|---|---|---|
| POST /v1/intents | – | ✓ (own tenant, budget-checked) | – | – | ✓ |
| POST /v1/intents/{id}/candidates | – | ✓ (own intent only) | – | – | ✓ |
| GET /v1/{intents,candidates,clusters,dossier} | – | ✓ own tenant | – | – | ✓ |
| POST /commands (internal) | – | – | – | – | svc-only |
| POST /v1/hooks/github | HMAC-gated (not role-based) | | | | |
| POST /fleet/jobs/claim | – | – | ✓ (service token) | – | – |
| POST /fleet/jobs/{id}/heartbeat·complete | – | – | ✓ lease JWT + matching fence | – | – |
| Upload artifacts / evidence payload | – | – | ✓ lease jti + fence, digest-bound | – | – |
| Check write-back (ghconn) | – | – | – | ✓ (event-driven only, from DecisionRendered) | – |
| Rotate keys / revoke agent / kill switch / force-expire quarantine | – | – | – | – | ✓ |

Denied-by-default everywhere; unknown routes behave identically to denied routes (no oracle).

### 3.4 Tenant isolation — specific enforcement points

1. **Ingest**: principal→`tenant_id` resolution happens BEFORE any proxying; tenant comes only from the verified token, never from payload body. Fail-closed when installation missing (EC-008).
2. **Query layer (primary)**: every sqlc query takes `tenant_id` as a parameter and filters by it; lint rule fails build on any hand-written query touching tenant-owned tables without the predicate.
3. **DB layer (backstop)**: Postgres RLS enabled on all tenant-owned tables; policy `USING (tenant_id = current_setting('app.tenant_id')::text)`; request paths connect as role `sauron_app` (RLS-bound, per-tx `SET app.tenant_id` from verified principal); `sauron_system` (migrations, replay, chain verifier) is separate, audited, used only by background jobs. Answer: **both layers**, deliberately.
4. **Storage layer**: artifact/object keys prefixed `tenant/{tenant_id}/trust/{env_class}/…`; fetch validates prefix against lease claims.
5. **Presentation**: uniform 404 mapping (T5); no tenant identifiers in error strings or metrics labels beyond hashed forms.

---

## 4. Secret handling policy

### 4.1 Never-stored / never-logged (absolute)

- **Never collected**: developer PATs; OAuth user tokens; CI secret values (Actions secrets, Buildkite env); cloud account credentials; SSH keys; full environment dumps. Onboarding Trust Center states this contractually (spec §Security onboarding).
- **Never present in execution (B5)**: no GitHub installation tokens, no DB credentials, no signing keys, no platform API keys. Jobs get code + declared fixtures only. Any future need (e.g. private module fetch) = brokered short-lived scoped grant, P2, never env-injected broad tokens.
- **Never logged**: any token/key material; redaction-class matches; file contents >2-line context; customer code beyond top-level dir (extends SRE §6.2 conventions).

### 4.2 Redaction pipeline — placement and spec

Order of defense: **ingest-time** (inbound payloads) → **emit-time** (every service's log/metric writer) → **store-time** (execution logs before artifact store) → **model-gate** (precondition for any LLM call; consumes only redacted views). Stages per pass: `DETECT → MASK (⟦REDACTED:<class>:n⟧) → COUNT (redaction_count metadata) → ENFORCE`. Fail-closed: scrubber error ⇒ quarantine-and-alert, never pass-through (matches SRE "log-and-drop").

Deviation flagged: ARCH §2.1 stores raw webhook payloads in `deliveries` as audit anchor. We redact pattern-matches at ingest anyway and hash the redacted form — originals remain re-fetchable from GitHub (HMAC-verified re-delivery), so forensic fidelity survives. Raw-secret persistence risk > marginal forensic loss.

| Pattern class | Example matcher | Notes |
|---|---|---|
| GitHub tokens | `gh[pousr]_\w{36,}`, `github_pat_\w{20,}` | incl. fine-grained |
| Cloud keys | `AKIA[0-9A-Z]{16}`, `AIza[\w-]{35}` | AWS, Google |
| Bearer/JWT | `eyJ\w+\.\w+\.\w+`, `Authorization:\s*Bearer\s+\S+` | three-segment JWTs |
| PEM | `-----BEGIN [A-Z ]*PRIVATE KEY-----` block | mask whole block |
| URL creds | `[a-z]+://[^/\s:@]+:[^@\s]+@` | userinfo form |
| Slack/others | `xox[abprs]-\w+` | extensible registry |
| Generic assignments | `(?i)(secret|password|api[_-]?key|token)\s*[:=]\s*\S{16,}` | entropy-scored to cut FPs |
| Repo-custom | regexes from `.integration/agent-control.yaml` | tenant-supplied, validated |

### 4.3 Env-var hygiene (compose/dev)

`.env` gitignored; `.env.example` placeholders only; compose interpolates `${VAR}` — never literals; `scripts/dev-keys.sh` mints throwaway Ed25519 signing keys + webhook secret into `.env`; no real cloud/GitHub credentials permitted on demo instances; dogfood CI job greps the repo with our own pattern classes (self-scan gate); quarterly manual review that `.env.example` stays placeholder-complete.

---

## 5. Provenance & attestation design v1

### 5.1 Evidence record (what v1 "attestation" contains)

Control-plane evaluates runner-submitted results, then signs. Signed JSON (canonicalized, detached signature):

```json
{ "v":1, "kind":"tier1_unit", "verdict":"pass",
  "request_id":"val_..", "job_id":"job_..", "lease_jti":"..", "fence":17,
  "tenant_id":"..", "repo_id":"..", "base_sha":"..", "head_sha":"..",
  "artifact_digests":["sha256:.."], "logs_digest":"sha256:<redacted-log>",
  "builder_image":"<ref>@sha256:..", "toolchain":{"go":"1.23.x"},
  "inputs_hash":"sha256(base_sha|head_sha|lockfiles|flags|img_digest|policy_ver)",
  "policy_version":"policies/default/v3", "executed_at":"..",
  "signed_by":"sauron:evidence-v1/<key-id>", "sig":"ed25519:.." }
```

Policy version stamping is mandatory on every `decision.rendered` (ARCH §4.2) and every evidence record — decisions are reproducible against the exact policy that made them. Builder image referenced by immutable digest only.

### 5.2 Hash-chain checkpoints

Ledger `entry_hash` chain per ARCH §4.1. Every 10k events: `ledger_checkpoints(seq, entry_hash, sig)` signed by the platform checkpoint key. Nightly verifier recomputes chain on a replica; ANY mismatch ⇒ halt projections fail-closed, enter read-only mode, page (EC-056).

### 5.3 Key custody ladder (answers ARCH §9 / SRE §10 open question)

| Phase | Evidence-signing key | Checkpoint key | Custody |
|---|---|---|---|
| v1 dev/demo | Ed25519 dev key, generated by script, env-held, rotated freely | same | Explicitly throwaway; docs state signatures prove pipeline logic, not infra trust |
| v1 pilot | Ed25519, file-permission-guarded, offline backup, dual-control rotation | separate key | Named owner (platform eng lead); rotation runbook |
| Phase 2 | Per-deployment key in cloud KMS (HSM-backed) | KMS key w/ automatic yearly rotation | IaC-managed; optional per-tenant keys (§9 Q2) |

### 5.4 Verification procedure (any auditor/verifier)

1. Fetch evidence record + its ledger entries (correlation chain from `delivery.accepted` → … → `evidence.recorded` → `decision.rendered`).
2. Verify Ed25519 signature against published platform key (key-id pinned in record).
3. Verify chain linkage: recompute `entry_hash`s across the correlation span; verify latest checkpoint signature covers span end.
4. Recompute `payload_sha256` / artifact digests against stored objects; recompute `logs_digest` from stored redacted log.
5. Confirm `policy_version` exists and `inputs_hash` matches decision-time freshness re-check (EC-032). Any failure ⇒ evidence quarantined, incident (EC-038).

### 5.5 Upgrade path toward SLSA-style attestations

Field mapping is deliberate: `artifact_digests`=in-toto subjects; `builder_image`+`toolchain`+recipe=SLSA `resolvedDependencies`/builder; `inputs_hash`=externalParameters; lease/fence=byproducts. Phase 2 wraps the same payload as a DSSE envelope with predicateType `slsa.dev/provenance/v1` once Firecracker builders can truthfully claim hermetic/isolated builds (SLSA Build L2→L3). Optional Rekor transparency-log anchoring of checkpoints afterwards. No field renames — additive evolution only.

---

## 6. Execution isolation roadmap

### 6.1 v1 — sim provider + local Docker: **NOT FOR PRODUCTION**

The simulator executes deterministic scripted workloads (no untrusted code) — acceptable everywhere. The **Docker provider runs untrusted agent patches and is approved for local development and demos ONLY**. It must never host real customer repos, real secrets, or multi-tenant data. Compensating controls (isolation-by-construction, since the isolation boundary itself is weak):

1. **No secrets in scope at all** — the strongest control is absence (§4.1). Demo instances carry zero real credentials.
2. **Network**: job containers attach ONLY to a per-job internal docker network with no external route; no `--network host`; default-bridge disabled in daemon config.
3. **No escapes-in-waiting**: docker socket never mounted into job containers; no privileged mode; `--cap-drop=ALL`; non-root uid; read-only rootfs with tmpfs scratch; `--pids-limit`, `--memory`, `--cpus` caps per tier.
4. **Filesystem**: only a scoped workspace bind (job dir, uid-mapped); no host mounts beyond it.
5. **Ephemerality + cleanup verification**: one container per job; post-job sweep asserts container/volume/network removal, records `cleanup_verified=true` into the completion event; leaked-resource reconciler alert (extends SRE §5 env sweep).
6. **Cache discipline** (T7): keys namespaced by tenant/repo/trust-domain; inputs-hash completeness property-tested (EC-034/054 automatable NOW — these controls don't need microVMs).
7. **Trust domains simulated**: `public_fork` class jobs get zero-network + zero-credential profile even in v1, so the policy plumbing is exercised before real isolation exists.

### 6.2 Phase 2 — hardened execution

Firecracker microVMs (or gVisor-sanitized K8s pods as interim), one VM per job; per-job workload identity via OIDC federation (cloud registries/deploy targets — no standing creds, spec secret-model); egress enforced by an allowlist proxy OUTSIDE the guest (package registries per repo policy; cloud-metadata IPs denied; guest-root cannot disable); Kubernetes NetworkPolicies; physical trust-domain pools: `public_fork` / `private` / `release` never share hosts or caches; attested cleanup; image pull by digest with signature verification.

### 6.3 Graduation criteria v1 → Phase 2 → production-execution

- [ ] Egress enforcement point is outside guest privilege boundary (verified by escape-attempt test suite run in CI).
- [ ] No shared kernel between untrusted code and platform components (gVisor or microVM).
- [ ] Credential presence scan proves zero secrets in guest env/images (automated, per-job).
- [ ] Trust domains physically separated; cache keys include trust domain; cross-domain fetch test fails closed.
- [ ] Cleanup attestation recorded and reconciled for 100% of jobs over a soak week.
- [ ] Builder produces SLSA-provable provenance (DSSE, §5.5) — required before ANY autonomous merge authority (Level 4+) ships.
- [ ] Chaos suite (SRE §7.2) green including runner-compromise scenarios.

Until every box is checked: banner in UI, README, and Trust Center; docker provider refuses to start if instance reports `mode=production`.

---

## 7. Audit & non-repudiation

**Append-only mechanics**: `control.ledger` grants are INSERT/SELECT only for ALL service roles — UPDATE/DELETE technically impossible without explicit DBA action (which is itself an audited, alarmed event via PG logging). Corrections happen by appending compensating events (e.g. `evidence.invalidated`), never by mutation. Deliveries likewise insert-only.

**Security-audit-grade events** (must land in ledger, retained forever): HMAC failures & lockouts (EC-003); replay-window rejects (EC-004); authz denials incl. cross-tenant probes (aggregated, EC-050); credential over-scope attempts (EC-049); fence violations (EC-046); evidence tamper/quarantine (EC-038/041); repair path violations (EC-020); budget kill-switch toggles; key/secret rotations; checkpoint verification failures (EC-056); quarantine create/expire; policy override requests (human exception flow).

**Non-repudiation properties**: (a) workers submit DATA, control-plane authors FACTS — a compromised runner cannot write audit history, only garbage results that fencing + attribution contain (T8); (b) every ledger entry carries actor + causation + correlation IDs (envelope, ARCH §4.1) ⇒ complete why-chains; (c) signed checkpoints make silent truncation/rewrite detectable.

**Retention**: ledger + checkpoints infinite (partitioned); security-audit-grade events inherit that forever-retention; `deliveries` raw 30d but security-reject rows kept 1y; execution logs 14d with digests forever; `processed_events` 90d. Export path (JSONL) required for customer incident response by pilot stage.

---

## 8. Security gates for builders (each service must pass before merge)

**Input validation**: all external input schema-validated with size caps and enum allowlists (EC-005/006/007 behaviors tested); glob/path inputs (surfaces, allowed_paths) checked against traversal (`..`, absolute, symlink tricks); sqlc-only SQL — no string-built queries; response bodies share one error mapper (uniform 404, no internals leaked).

**TLS posture**: v1 compose = plain HTTP inside the single trusted network boundary, DOCUMENTED as such; anything leaving the host (GitHub API, future cloud) = TLS with cert verification ON; Phase 2 = mTLS on all B3 links. Gate: config lint fails if a prod-mode build disables cert verification anywhere.

**Dependency policy**: go.sum pinned; `govulncheck` clean in CI; new dependency requires reviewed justification in PR template; base images pinned by digest; dependabot/renovate alerts triaged weekly; no dependencies with unresolved criticals ship.

**Rate limits**: ingest per-source and per-tenant buckets (429 + Retry-After); agent API per-key; fleet claim throttle per worker; admin ops rate-limited; limits themselves load-tested in storm profile (SRE §7.1).

**Security-relevant tests each service ships** (mapping EDGE_CASES): ingest → EC-001–011 suite incl. concurrent dedupe race + secret-rotation window; control-plane → EC-020/033–041 invariants (skip≠pass, inputs-hash reuse, single-acceptance, SHA binding, tamper quarantine, quarantine-abuse scenario) + EC-050 uniform-404 + redaction unit tests (fixture corpus per §4.2 class, FPR threshold); runner-fleet → EC-042–049 (silent death, stale fence, double claim, OOM residue, credential over-scope); cross-cutting → chain-break detection (EC-056), idempotent-apply property (EC-062). A service PR that touches authz, redaction, lease claims, or evidence acceptance additionally requires a named human reviewer from the security roster (CODEOWNERS-enforced).

---

## 9. Open questions

1. **Architect/SRE**: checkpoint + evidence key custody sign-off for PILOT (not just dev) — is file-guarded dual-control acceptable pre-KMS, and who is the named key owner?
2. **Architect/domain**: per-tenant evidence-signing keys (customer-verifiable, HSM-costly) vs platform keys (simpler, weaker tenancy story) — decide before first regulated customer.
3. **SRE**: does dropping Redis (SRE §9) change any rate-limit/budget atomicity assumptions in §2-T4 countermeasures? (Believed no — PG CAS suffices; confirm.)
4. **Architect**: mTLS timing for B3 — adopt service mesh/certs at Phase 2 cutover, or lightweight mutual-token scheme earlier?
5. **Domain/SRE**: webhook secret rotation runbook ownership and customer-visible SLA for emergency rotation (assumed: <15 min, connector supports dual-secret window).
6. **Domain/legal**: is cross-tenant artifact reuse EVER permissible (identical dependency subtrees) or hard-isolation forever? Blocks T7 residual-risk framing.
7. **Security/business**: which LLM vendors meet the redaction-before-model-use DPA bar for failure summarization; is self-hosted inference required for regulated tiers?
8. **Product/architect**: when do public-repo/fork PRs become supported? Drives urgency of physical trust-domain separation (§6.2) — currently Phase 2, becomes P0 if forks arrive early.
9. **SRE**: retention for security-audit-grade HTTP-layer denials (currently: aggregate in ledger forever, raw rows 1y) — legal/compliance sign-off needed.
