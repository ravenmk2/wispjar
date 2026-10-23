CREATE TABLE search_provider (
  id CHAR(32) NOT NULL PRIMARY KEY,
  name VARCHAR(128) NOT NULL,
  type VARCHAR(32) NOT NULL,
  base_url VARCHAR(512) NOT NULL,
  api_key_enc BLOB NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_by CHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE search_config (
  id TINYINT NOT NULL PRIMARY KEY,
  default_provider_id CHAR(32) NULL,
  CONSTRAINT fk_search_default FOREIGN KEY (default_provider_id) REFERENCES search_provider(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO search_config (id, default_provider_id) VALUES (1, NULL);

ALTER TABLE agent ADD COLUMN search_provider_id CHAR(32) NULL AFTER tools_enabled,
  ADD CONSTRAINT fk_agent_search_provider FOREIGN KEY (search_provider_id) REFERENCES search_provider(id);

CREATE TABLE search_call (
  id CHAR(32) NOT NULL PRIMARY KEY,
  provider_id CHAR(32) NOT NULL,
  org_id CHAR(32) NOT NULL DEFAULT '',
  user_id CHAR(32) NOT NULL DEFAULT '',
  agent_id CHAR(32) NOT NULL DEFAULT '',
  session_id CHAR(32) NOT NULL DEFAULT '',
  call_id VARCHAR(128) NOT NULL DEFAULT '',
  success BOOLEAN NOT NULL,
  result_count INT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL,
  credits DOUBLE NULL,
  error_code VARCHAR(32) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_search_call_provider (provider_id, created_at),
  KEY idx_search_call_org (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
