-- The session sandbox architecture is retired. Legacy sessions and workspace
-- migration operations are deliberately discarded, not converted to computers.
-- Stop the application and all remote executions before applying this migration.
-- Use an ordinary short-lived table: the application has schema DDL rights,
-- but does not need the separate CREATE TEMPORARY TABLES privilege.
CREATE TABLE retired_session (id CHAR(32) PRIMARY KEY)
  ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO retired_session (id)
SELECT s.id FROM session s
WHERE s.computer_id IS NULL
   OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(s.sandbox, '$.id')), '') <> ''
   OR EXISTS (SELECT 1 FROM session_event e WHERE e.session_id = s.id AND e.type = 'session/sandbox');

START TRANSACTION;
DELETE l FROM run_skill_lease l
JOIN computer_run r ON r.id = l.run_id
LEFT JOIN retired_session s ON s.id = r.session_id
WHERE s.id IS NOT NULL
   OR r.id IN (
     SELECT JSON_UNQUOTE(JSON_EXTRACT(progress, '$.maintenance_run_id'))
     FROM resource_operation WHERE action = 'workspace/migrate'
   );
DELETE r FROM computer_run r LEFT JOIN retired_session s ON s.id = r.session_id
WHERE s.id IS NOT NULL OR r.id IN (
  SELECT JSON_UNQUOTE(JSON_EXTRACT(progress, '$.maintenance_run_id'))
  FROM resource_operation WHERE action = 'workspace/migrate'
);
DELETE e FROM session_event e JOIN retired_session s ON s.id = e.session_id;
DELETE c FROM agent_checkpoint c JOIN retired_session s ON s.id = c.session_id;
DELETE FROM resource_operation WHERE action = 'workspace/migrate';
DELETE s FROM session s JOIN retired_session old ON old.id = s.id;
COMMIT;
DROP TABLE retired_session;

ALTER TABLE session
  DROP COLUMN sandbox,
  MODIFY COLUMN computer_id CHAR(32) NOT NULL;
