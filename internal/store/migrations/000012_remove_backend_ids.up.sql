ALTER TABLE user_volume
  ADD UNIQUE KEY uk_user_volume_backend_name (backend_name),
  DROP INDEX uk_user_volume_backend,
  DROP COLUMN backend_id;

ALTER TABLE computer DROP COLUMN backend_id;
