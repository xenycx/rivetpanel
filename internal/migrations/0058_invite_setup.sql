-- Account invitations carry the new account's setup. Purely additive; every
-- column is nullable, so existing invitation rows and links keep working.
--
-- role_id: a custom role (account_invites.role = 'user' plus role_id, like
-- users). quota_json: limits for the invitee's personal workspace, a JSON
-- object with the workspace_quotas field names. node_id: the node new apps
-- in that personal workspace are placed on. workspace_id and workspace_role:
-- a team workspace the invitee joins, and as what. A role, node or workspace
-- deleted before the link is used becomes NULL and is skipped on
-- registration. The application applies these when the invitation is used.
ALTER TABLE account_invites ADD COLUMN role_id TEXT
    REFERENCES roles(id) ON DELETE SET NULL;
ALTER TABLE account_invites ADD COLUMN quota_json TEXT
    CHECK (quota_json IS NULL OR (json_valid(quota_json) AND json_type(quota_json) = 'object'
                                  AND length(quota_json) <= 1024));
ALTER TABLE account_invites ADD COLUMN node_id TEXT
    REFERENCES nodes(id) ON DELETE SET NULL;
ALTER TABLE account_invites ADD COLUMN workspace_id TEXT
    REFERENCES workspaces(id) ON DELETE SET NULL;
ALTER TABLE account_invites ADD COLUMN workspace_role TEXT
    CHECK (workspace_role IS NULL OR workspace_role IN ('admin', 'developer', 'viewer'));
