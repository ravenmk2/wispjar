-- Rename only the original built-in defaults and preserve all custom names.
UPDATE organization
SET name = '系统管理'
WHERE id = '00000000000000000000000000000001'
  AND is_platform = 1
  AND BINARY name = BINARY '平台管理机构';

UPDATE user AS u
SET u.display_name = '系统管理员'
WHERE BINARY u.username = BINARY 'admin'
  AND BINARY u.display_name = BINARY '平台管理员'
  AND EXISTS (
    SELECT 1 FROM org_member AS m
    WHERE m.user_id = u.id
      AND m.org_id = '00000000000000000000000000000001'
      AND m.role = 'admin'
  );
