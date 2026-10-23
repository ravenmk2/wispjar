-- Keep existing instance IDs, encrypted keys, and search selections intact.
ALTER TABLE search_provider ADD COLUMN timeout_seconds INT NOT NULL DEFAULT 20;
ALTER TABLE search_config ADD COLUMN default_fetch_provider_id CHAR(32) NULL,
  ADD CONSTRAINT fk_fetch_default FOREIGN KEY (default_fetch_provider_id) REFERENCES search_provider(id);
ALTER TABLE agent ADD COLUMN fetch_provider_id CHAR(32) NULL AFTER search_provider_id,
  ADD CONSTRAINT fk_agent_fetch_provider FOREIGN KEY (fetch_provider_id) REFERENCES search_provider(id);

INSERT INTO search_provider (id, name, type, base_url, api_key_enc, status, created_by)
VALUES ('00000000000040008000000000000001', 'Go HTTP（内置）', 'go_http', '', X'', 'active', '');
UPDATE search_config SET default_fetch_provider_id = '00000000000040008000000000000001' WHERE id = 1;

CREATE TABLE fetch_call (
  id CHAR(32) NOT NULL PRIMARY KEY,
  provider_id CHAR(32) NOT NULL,
  org_id CHAR(32) NOT NULL DEFAULT '',
  user_id CHAR(32) NOT NULL DEFAULT '',
  agent_id CHAR(32) NOT NULL DEFAULT '',
  session_id CHAR(32) NOT NULL DEFAULT '',
  call_id VARCHAR(128) NOT NULL DEFAULT '',
  success BOOLEAN NOT NULL,
  content_chars INT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL,
  error_code VARCHAR(32) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_fetch_call_provider (provider_id, created_at),
  KEY idx_fetch_call_org (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
