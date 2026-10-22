CREATE TABLE IF NOT EXISTS attachment_access (
  org_id CHAR(32) NOT NULL,
  attachment_id CHAR(71) NOT NULL,
  name VARCHAR(256) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (org_id, attachment_id),
  CONSTRAINT fk_attachment_access_org FOREIGN KEY (org_id) REFERENCES organization(id),
  CONSTRAINT fk_attachment_access_blob FOREIGN KEY (attachment_id) REFERENCES attachment(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO attachment_access (org_id, attachment_id, name, created_at)
SELECT org_id, id, name, created_at FROM attachment;

CREATE TABLE IF NOT EXISTS provider_media_file (
  scope CHAR(64) NOT NULL,
  attachment_id CHAR(71) NOT NULL,
  file_id VARCHAR(256) NOT NULL,
  expires_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (scope, attachment_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
