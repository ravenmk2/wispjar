-- 平台级沙箱默认环境变量（{KEY: "value"}，创建沙箱时注入；agent.sandbox_config.env 逐键覆盖）。
INSERT INTO platform_setting (`key`, value, updated_by) VALUES
  ('sandbox.env', '{}', 'system');
