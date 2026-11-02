CREATE TABLE model_request_log_setting (
    model_id CHAR(32) NOT NULL PRIMARY KEY,
    enabled TINYINT(1) NOT NULL DEFAULT 0,
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE model_request_log (
    id CHAR(32) NOT NULL PRIMARY KEY,
    started_at DATETIME(3) NOT NULL,
    model_id CHAR(32) NOT NULL,
    provider_id CHAR(32) NOT NULL,
    status VARCHAR(16) NOT NULL,
    payload_path VARCHAR(255) NOT NULL,
    payload_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
    payload_sha256 CHAR(64) NOT NULL DEFAULT '',
    metadata JSON NOT NULL,
    error_detail JSON NULL,
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_started (started_at, id),
    KEY idx_model_started (model_id, started_at, id),
    KEY idx_status_updated (status, updated_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
