-- Reverse of 0014. WHY drop extension too: citext was introduced BY this
-- migration; no other ctrl table uses it (checked pre-0014 schema), so removal
-- keeps fresh-volume parity with pre-auth deployments.
BEGIN;

DROP TABLE IF EXISTS ctrl.users;
DROP EXTENSION IF EXISTS citext;

COMMIT;
