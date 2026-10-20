-- Schema rollback does NOT delete Docker volumes or the installation identity.
-- Export resource identities before rollback; new workspace writes require a
-- separate data rollback plan, not merely this schema migration.
ALTER TABLE skill DROP COLUMN content_hash, DROP COLUMN revision;
ALTER TABLE session DROP INDEX idx_session_computer, DROP COLUMN workspace, DROP COLUMN computer_id;
DROP TABLE resource_operation;
DROP TABLE run_skill_lease;
DROP TABLE computer_run;
DROP TABLE user_skill_state;
DROP TABLE user_agent_binding;
DROP TABLE user_volume;
DROP TABLE computer;
