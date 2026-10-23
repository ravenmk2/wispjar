-- Downgrading discards fetch configuration and usage. Run only after review.
DROP TABLE fetch_call;
ALTER TABLE agent DROP FOREIGN KEY fk_agent_fetch_provider, DROP COLUMN fetch_provider_id;
ALTER TABLE search_config DROP FOREIGN KEY fk_fetch_default, DROP COLUMN default_fetch_provider_id;
DELETE FROM search_provider WHERE type = 'go_http';
ALTER TABLE search_provider DROP COLUMN timeout_seconds;
