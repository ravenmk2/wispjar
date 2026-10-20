-- Additive migration. No backend resources are created or deleted by SQL.
CREATE TABLE computer (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  name VARCHAR(128) NOT NULL,
  backend_id VARCHAR(64) NOT NULL,
  sandbox_id VARCHAR(128) NULL,
  generation BIGINT UNSIGNED NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'stopped',
  config JSON NOT NULL,
  last_active_at DATETIME(3) NULL,
  last_error TEXT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_computer_owner (org_id, user_id, status),
  KEY idx_computer_sandbox (sandbox_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_volume (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  kind VARCHAR(16) NOT NULL,
  backend_id VARCHAR(64) NOT NULL,
  backend_name VARCHAR(63) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  policy JSON NOT NULL,
  `usage` JSON NOT NULL,
  last_error TEXT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_user_volume_owner (org_id, user_id, kind),
  UNIQUE KEY uk_user_volume_backend (backend_id, backend_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_agent_binding (
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  agent_id CHAR(32) NOT NULL,
  computer_id CHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (org_id, user_id, agent_id),
  KEY idx_user_agent_binding_computer (computer_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_skill_state (
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  skill_id CHAR(32) NOT NULL,
  desired_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  applied_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  lock_owner VARCHAR(128) NOT NULL DEFAULT '',
  lock_until DATETIME(3) NULL,
  fence BIGINT UNSIGNED NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (org_id, user_id, skill_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE computer_run (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  computer_id CHAR(32) NOT NULL,
  session_id CHAR(32) NOT NULL,
  parent_run_id CHAR(32) NULL,
  agent_id CHAR(32) NOT NULL,
  status VARCHAR(16) NOT NULL,
  owner VARCHAR(128) NOT NULL DEFAULT '',
  lease_until DATETIME(3) NULL,
  execution JSON NOT NULL,
  manifest JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_computer_run_computer (computer_id, status),
  KEY idx_computer_run_session (session_id, status),
  KEY idx_computer_run_parent (parent_run_id),
  KEY idx_computer_run_lease (status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE run_skill_lease (
  run_id CHAR(32) NOT NULL,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  skill_id CHAR(32) NOT NULL,
  revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id, skill_id),
  KEY idx_run_skill_lease_skill (org_id, user_id, skill_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE resource_operation (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  resource_type VARCHAR(32) NOT NULL,
  resource_id CHAR(32) NOT NULL,
  action VARCHAR(64) NOT NULL,
  status VARCHAR(16) NOT NULL,
  idempotency_key VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  request_hash CHAR(64) NOT NULL DEFAULT '',
  progress JSON NOT NULL,
  error TEXT NOT NULL,
  lease_until DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_resource_operation_idempotency (org_id, user_id, action, idempotency_key),
  KEY idx_resource_operation_lease (status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE session
  ADD COLUMN computer_id CHAR(32) NULL,
  ADD COLUMN workspace JSON NULL,
  ADD KEY idx_session_computer (computer_id);
ALTER TABLE skill
  ADD COLUMN revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  ADD COLUMN content_hash CHAR(64) NOT NULL DEFAULT '';
