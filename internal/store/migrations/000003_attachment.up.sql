CREATE TABLE attachment (
  id         CHAR(71)     NOT NULL PRIMARY KEY,            -- "sha256:" + 64 位小写 hex（内容寻址，全局去重）
  org_id     CHAR(32)     NOT NULL,
  mime       VARCHAR(64)  NOT NULL,                        -- image/png / image/jpeg / image/webp / image/gif
  bytes      BIGINT       NOT NULL,
  width      INT          NOT NULL,
  height     INT          NOT NULL,
  name       VARCHAR(256) NOT NULL DEFAULT '',
  created_by CHAR(32)     NOT NULL,
  created_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_attachment_org (org_id, created_at),
  CONSTRAINT fk_attachment_org FOREIGN KEY (org_id) REFERENCES organization(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
