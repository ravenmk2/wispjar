ALTER TABLE session
  DROP COLUMN model_params,
  MODIFY COLUMN approval_mode VARCHAR(16) NOT NULL DEFAULT 'default';
