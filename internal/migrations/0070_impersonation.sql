-- Administrator impersonation. A session with impersonator_id set was opened
-- by that administrator to act as the session's user. The column is checked
-- on every request: the session stops working when the administrator is
-- disabled, loses the administrator role or is deleted (the cascade removes
-- it). Purely additive: existing sessions read as ordinary sign-ins.
ALTER TABLE sessions ADD COLUMN impersonator_id TEXT
    REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX sessions_impersonator_idx ON sessions(impersonator_id)
    WHERE impersonator_id IS NOT NULL;
