BEGIN;

ALTER TABLE ctrl.command_log
    ALTER COLUMN response_body SET DATA TYPE jsonb USING
        convert_from(response_body, 'UTF8')::jsonb;

COMMIT;
