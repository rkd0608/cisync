-- ctrl 0014: user accounts for classic email+password sign-in (SPEC §3,
-- 2026-08-26 auth-engineer row — replaces OTP-email delivery entirely).
--
-- WHY citext: case-insensitive UNIQUE on email so Dev@example.com and
-- dev@example.com can never both exist. WHY app-minted ULIDs instead of a
-- PG extension default: no pgx_ulid extension ships in the postgres image;
-- oklog/ulid/v2 is already a direct control-plane dependency, and ULIDs minted
-- at INSERT time keep lexicographic = chronological ordering without an
-- extension lifecycle. WHY no sessions table: sessions are stateless compact
-- JWTs (30d) signed with the dedicated session key (B2-style trust-domain
-- separation); revocation = key rotation, logout = cookie clearing.

BEGIN;

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE ctrl.users (
    id            text        PRIMARY KEY,
    tenant_id     text        NOT NULL DEFAULT 'org_01ARZ3NDEKTSV4RRFFQ69G5FAV',
    email         citext      NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CONSTRAINT users_email_unique UNIQUE (email)
);

COMMIT;
