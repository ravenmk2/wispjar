ALTER TABLE session
  ADD COLUMN model_params JSON NULL AFTER model_id,
  MODIFY COLUMN approval_mode VARCHAR(16) NOT NULL DEFAULT 'auto';
