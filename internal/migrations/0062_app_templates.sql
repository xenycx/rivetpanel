-- One-click app templates, container web addresses and trusted capabilities.
--
-- stacks.template_id records which built-in app template a stack was
-- installed from ('' for a pasted Compose file). bot_containers.routable puts
-- a container app on the shared apps network so a site (web address) can
-- proxy to it. bot_containers.cap_add_json holds the extra Linux capabilities
-- a built-in template asked for; the runtime filters them again against a
-- fixed allowlist, and pasted Compose files can never set them.

ALTER TABLE stacks ADD COLUMN template_id TEXT NOT NULL DEFAULT ''
    CHECK (length(template_id) <= 64);

ALTER TABLE bot_containers ADD COLUMN routable INTEGER NOT NULL DEFAULT 0
    CHECK (routable IN (0, 1));

ALTER TABLE bot_containers ADD COLUMN cap_add_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(cap_add_json) AND json_type(cap_add_json) = 'array');
