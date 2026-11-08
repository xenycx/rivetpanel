-- The bots.configure role permission ("Configuration and startup"). Purely
-- additive.
--
-- roles: the built-in User role holds every resource permission by default
-- (domain.DefaultUserPermissions), so it gains bots.configure, placed after
-- bots.env to keep catalog order. Custom roles are left alone: a custom role
-- that holds console, power, files and variables already could change a
-- bot's startup configuration, and one that does not should not gain it
-- silently.
UPDATE roles SET permissions_json = (
  SELECT json_group_array(value) FROM (
    SELECT value, CAST(key AS REAL) AS k FROM json_each(roles.permissions_json)
    UNION ALL
    SELECT 'bots.configure', COALESCE(
      (SELECT CAST(key AS REAL) FROM json_each(roles.permissions_json) WHERE value = 'bots.env'),
      (SELECT COUNT(*) FROM json_each(roles.permissions_json))) + 0.5
    ORDER BY k
  )
)
WHERE id = 'user' AND system = 1
  AND NOT EXISTS (SELECT 1 FROM json_each(roles.permissions_json) WHERE value = 'bots.configure');
