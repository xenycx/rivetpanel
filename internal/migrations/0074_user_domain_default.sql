-- A user's default domain for new sites. Additive: existing domains are not
-- the default (is_default 0). At most one per user, kept by the application
-- and this partial unique index.
ALTER TABLE user_domains ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1));
CREATE UNIQUE INDEX user_domains_default_idx ON user_domains(user_id) WHERE is_default = 1;
