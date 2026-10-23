-- Schema-only reversal. Discarded legacy sessions cannot be recovered.
ALTER TABLE session
  ADD COLUMN sandbox JSON NULL,
  MODIFY COLUMN computer_id CHAR(32) NULL;
UPDATE session SET sandbox = JSON_OBJECT('id', '', 'status', 'none');
ALTER TABLE session MODIFY COLUMN sandbox JSON NOT NULL;
