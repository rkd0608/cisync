-- I-12 byte-level replay identity: response_body was jsonb, which
-- renormalizes JSON on round-trip (key reordering + spacing), so idempotent
-- replays returned a DIFFERENT byte stream than the original response.
-- Store the exact bytes served instead.
BEGIN;

ALTER TABLE ctrl.command_log
    ALTER COLUMN response_body SET DATA TYPE bytea USING
        convert_to(response_body::text, 'UTF8');

COMMIT;
