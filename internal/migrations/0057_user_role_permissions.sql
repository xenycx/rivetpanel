-- The built-in User role becomes configurable. Purely additive.
--
-- roles: until now the application ignored the stored list of the 'user'
-- system row and granted every resource permission. This writes that list
-- (plus the new workspaces.rename, which keeps the implicit ability to
-- rename workspaces an account administers) into the row, so the stored list
-- is what accounts with the built-in User role get from now on and built-in
-- administrators can change it. The administrator row stays computed ('["*"]'
-- is never read). The list is spelled out literally; a Go test keeps it equal
-- to domain.DefaultUserPermissions(), which "Restore defaults" restores.
UPDATE roles SET permissions_json = json_array(
  'bots.create','bots.delete','bots.console','bots.power','bots.files','bots.env',
  'bots.backups','bots.deploy','bots.share','sites.create','workspaces.create',
  'workspaces.rename','api_keys.manage','ai.use','tickets.create')
WHERE id = 'user' AND system = 1;

-- Custom roles gain workspaces.rename too, so nobody loses the ability to
-- rename workspaces they administer on upgrade.
UPDATE roles SET permissions_json = json_insert(permissions_json, '$[#]', 'workspaces.rename')
WHERE system = 0 AND NOT EXISTS (SELECT 1 FROM json_each(roles.permissions_json) WHERE value = 'workspaces.rename');
