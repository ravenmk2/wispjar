CREATE TABLE organization (
  id          CHAR(32)     NOT NULL PRIMARY KEY,
  name        VARCHAR(128) NOT NULL,
  is_platform TINYINT(1)   NOT NULL DEFAULT 0,
  status      VARCHAR(16)  NOT NULL DEFAULT 'active',
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE organization
  ADD COLUMN platform_guard CHAR(1) GENERATED ALWAYS AS (IF(is_platform=1,'1',NULL)) VIRTUAL,
  ADD UNIQUE KEY uk_org_platform_guard (platform_guard);

CREATE TABLE user (
  id                   CHAR(32)     NOT NULL PRIMARY KEY,
  username             VARCHAR(64)  NOT NULL,
  password_hash        VARCHAR(128) NOT NULL,
  display_name         VARCHAR(128) NOT NULL DEFAULT '',
  status               VARCHAR(16)  NOT NULL DEFAULT 'active',
  must_change_password TINYINT(1)   NOT NULL DEFAULT 0,
  created_at           DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at           DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_user_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE org_member (
  id         CHAR(32)    NOT NULL PRIMARY KEY,
  org_id     CHAR(32)    NOT NULL,
  user_id    CHAR(32)    NOT NULL,
  role       VARCHAR(16) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_member_org_user (org_id, user_id),
  KEY idx_member_user (user_id),
  CONSTRAINT fk_member_org  FOREIGN KEY (org_id)  REFERENCES organization(id),
  CONSTRAINT fk_member_user FOREIGN KEY (user_id) REFERENCES user(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE model_provider (
  id          CHAR(32)     NOT NULL PRIMARY KEY,
  name        VARCHAR(128) NOT NULL,
  type        VARCHAR(32)  NOT NULL,
  base_url    VARCHAR(512) NOT NULL,
  api_key_enc BLOB         NOT NULL,
  extra       JSON         NULL,
  status      VARCHAR(16)  NOT NULL DEFAULT 'active',
  created_by  CHAR(32)     NOT NULL,
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_provider_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE model (
  id             CHAR(32)     NOT NULL PRIMARY KEY,
  provider_id    CHAR(32)     NOT NULL,
  model_code     VARCHAR(128) NOT NULL,
  display_name   VARCHAR(128) NOT NULL,
  capabilities   JSON         NOT NULL,
  context_size   INT          NOT NULL DEFAULT 64000,
  default_params JSON         NULL,
  status         VARCHAR(16)  NOT NULL DEFAULT 'active',
  created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_model_provider_code (provider_id, model_code),
  CONSTRAINT fk_model_provider FOREIGN KEY (provider_id) REFERENCES model_provider(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE platform_setting (
  `key`      VARCHAR(64) NOT NULL PRIMARY KEY,
  value      JSON        NOT NULL,
  updated_by CHAR(32)    NOT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent (
  id             CHAR(32)      NOT NULL PRIMARY KEY,
  org_id         CHAR(32)      NOT NULL,
  name           VARCHAR(128)  NOT NULL,
  description    VARCHAR(1024) NOT NULL DEFAULT '',
  avatar         VARCHAR(512)  NOT NULL DEFAULT '',
  system_prompt  MEDIUMTEXT    NOT NULL,
  model_id       CHAR(32)      NOT NULL,
  model_params   JSON          NULL,
  max_iterations INT           NOT NULL DEFAULT 20,
  tools_enabled  JSON          NOT NULL,
  sandbox_config JSON          NULL,
  status         VARCHAR(16)   NOT NULL DEFAULT 'draft',
  created_by     CHAR(32)      NOT NULL,
  created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_agent_org_status (org_id, status),
  CONSTRAINT fk_agent_org   FOREIGN KEY (org_id)   REFERENCES organization(id),
  CONSTRAINT fk_agent_model FOREIGN KEY (model_id) REFERENCES model(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE skill (
  id           CHAR(32)      NOT NULL PRIMARY KEY,
  org_id       CHAR(32)      NOT NULL,
  name         VARCHAR(64)   NOT NULL,
  description  VARCHAR(1024) NOT NULL,
  meta         JSON          NOT NULL,
  storage_path VARCHAR(512)  NOT NULL,
  zip_size     BIGINT        NOT NULL DEFAULT 0,
  status       VARCHAR(16)   NOT NULL DEFAULT 'active',
  created_by   CHAR(32)      NOT NULL,
  created_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_skill_org_name (org_id, name),
  CONSTRAINT fk_skill_org FOREIGN KEY (org_id) REFERENCES organization(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_skill (
  agent_id CHAR(32) NOT NULL,
  skill_id CHAR(32) NOT NULL,
  sort     INT      NOT NULL DEFAULT 0,
  PRIMARY KEY (agent_id, skill_id),
  CONSTRAINT fk_askill_agent FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE,
  CONSTRAINT fk_askill_skill FOREIGN KEY (skill_id) REFERENCES skill(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_subagent (
  agent_id     CHAR(32) NOT NULL,
  sub_agent_id CHAR(32) NOT NULL,
  sort         INT      NOT NULL DEFAULT 0,
  PRIMARY KEY (agent_id, sub_agent_id),
  CONSTRAINT fk_asub_agent FOREIGN KEY (agent_id)     REFERENCES agent(id) ON DELETE CASCADE,
  CONSTRAINT fk_asub_sub   FOREIGN KEY (sub_agent_id) REFERENCES agent(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE session (
  id             CHAR(32)     NOT NULL PRIMARY KEY,
  org_id         CHAR(32)     NOT NULL,
  user_id        CHAR(32)     NOT NULL,
  agent_id       CHAR(32)     NOT NULL,
  agent_snapshot JSON         NOT NULL,
  title          VARCHAR(256) NOT NULL DEFAULT '',
  title_pinned   TINYINT(1)   NOT NULL DEFAULT 0,
  approval_mode  VARCHAR(16)  NOT NULL DEFAULT 'default',
  model_id       CHAR(32)     NOT NULL,
  sandbox        JSON         NOT NULL,
  last_seq       BIGINT       NOT NULL DEFAULT 0,
  running        TINYINT(1)   NOT NULL DEFAULT 0,
  usage_total    JSON         NOT NULL,
  status         VARCHAR(16)  NOT NULL DEFAULT 'active',
  last_active_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_session_user (org_id, user_id, status, last_active_at),
  CONSTRAINT fk_session_org   FOREIGN KEY (org_id)   REFERENCES organization(id),
  CONSTRAINT fk_session_user  FOREIGN KEY (user_id)  REFERENCES user(id),
  CONSTRAINT fk_session_agent FOREIGN KEY (agent_id) REFERENCES agent(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE session_event (
  session_id CHAR(32)    NOT NULL,
  seq        BIGINT      NOT NULL,
  type       VARCHAR(64) NOT NULL,
  time_ms    BIGINT      NOT NULL,
  data       JSON        NOT NULL,
  PRIMARY KEY (session_id, seq),
  CONSTRAINT fk_event_session FOREIGN KEY (session_id) REFERENCES session(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_checkpoint (
  id         CHAR(32)    NOT NULL PRIMARY KEY,
  session_id CHAR(32)    NOT NULL,
  data       MEDIUMBLOB  NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_checkpoint_session (session_id),
  CONSTRAINT fk_checkpoint_session FOREIGN KEY (session_id) REFERENCES session(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE usage_daily (
  date               DATE     NOT NULL,
  org_id             CHAR(32) NOT NULL,
  user_id            CHAR(32) NOT NULL,
  agent_id           CHAR(32) NOT NULL,
  model_id           CHAR(32) NOT NULL,
  requests           INT      NOT NULL DEFAULT 0,
  input_tokens       BIGINT   NOT NULL DEFAULT 0,
  output_tokens      BIGINT   NOT NULL DEFAULT 0,
  cache_read_tokens  BIGINT   NOT NULL DEFAULT 0,
  cache_write_tokens BIGINT   NOT NULL DEFAULT 0,
  reasoning_tokens   BIGINT   NOT NULL DEFAULT 0,
  PRIMARY KEY (date, org_id, user_id, agent_id, model_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE audit_log (
  id         CHAR(32)    NOT NULL PRIMARY KEY,
  org_id     CHAR(32)    NOT NULL DEFAULT '',
  actor_id   CHAR(32)    NOT NULL,
  action     VARCHAR(64) NOT NULL,
  target     VARCHAR(128) NOT NULL DEFAULT '',
  detail     JSON        NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_audit_org_time (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO organization (id, name, is_platform) VALUES
  ('00000000000000000000000000000001', '平台管理机构', 1);

INSERT INTO platform_setting (`key`, value, updated_by) VALUES
  ('sandbox.default_image',    '"python:3.12"',                'system'),
  ('sandbox.images',           '["python:3.12"]',              'system'),
  ('sandbox.egress_policy',    '{"defaultAction":"allow","rules":[]}', 'system'),
  ('sandbox.resource_limits',  '{"cpu":"1","memory":"1Gi"}',   'system'),
  ('sandbox.idle_ttl_seconds', '1800',                         'system'),
  ('sandbox.ttl_seconds',      '3600',                         'system');
