-- Domains a user has proven they control, reusable for any site. Additive.
--
-- user_domain_tokens: one domain-verification token per user, created on
-- first use. A TXT record _rivetpanel-verify.<name> = rivetpanel-verify=<token>
-- on a domain (or a parent of it, up to its registrable domain) proves the
-- user controls it and every name below it.
--
-- user_domains: the user's domains. A verified one is offered when creating
-- a site (served at <slug>.<domain>), and any custom domain at or below it
-- that the user adds to a site is verified without a further record.
--
-- site_domains.user_domain_id records which user domain proved a custom
-- domain (NULL: its own per-domain record). Removing the user domain clears
-- the link; the custom domain then needs its own record at the next check.
CREATE TABLE user_domain_tokens (
    user_id        TEXT PRIMARY KEY NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token          TEXT NOT NULL CHECK (length(token) = 32),
    created_at_ms  INTEGER NOT NULL
) STRICT;

CREATE TABLE user_domains (
    id                  TEXT PRIMARY KEY NOT NULL,
    user_id             TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain              TEXT NOT NULL CHECK (length(domain) BETWEEN 4 AND 253),
    verified_at_ms      INTEGER,
    last_checked_at_ms  INTEGER,
    last_error          TEXT,
    created_at_ms       INTEGER NOT NULL,
    UNIQUE (user_id, domain)
) STRICT;
CREATE INDEX user_domains_domain_idx ON user_domains(domain);

ALTER TABLE site_domains ADD COLUMN user_domain_id TEXT REFERENCES user_domains(id) ON DELETE SET NULL;
