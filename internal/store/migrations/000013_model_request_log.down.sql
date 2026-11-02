DROP TABLE IF EXISTS model_request_log;
DROP TABLE IF EXISTS model_request_log_setting;
DELETE FROM platform_setting WHERE `key` = 'model_request_log';
