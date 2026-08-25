BEGIN;
DROP TABLE IF EXISTS ghconn.repo_delivery_observations;
DROP INDEX IF EXISTS ghconn.uq_check_reports_live_revision;
ALTER TABLE ghconn.check_reports DROP COLUMN IF EXISTS live;
DROP TABLE IF EXISTS ghconn.installation_repos;
ALTER TABLE ghconn.installations DROP COLUMN IF EXISTS suspended;
ALTER TABLE ghconn.installations DROP COLUMN IF EXISTS permissions;
ALTER TABLE ghconn.installations DROP COLUMN IF EXISTS updated_at;
COMMIT;
