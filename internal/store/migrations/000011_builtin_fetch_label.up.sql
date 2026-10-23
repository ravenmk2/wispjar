-- Preserve user-renamed instances and all provider IDs, selections and keys.
UPDATE search_provider SET name = '内置网页读取', updated_at = updated_at
WHERE id = '00000000000040008000000000000001' AND type = 'go_http' AND name = 'Go HTTP（内置）';
