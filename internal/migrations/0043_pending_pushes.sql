-- A GitHub push that arrived while the server's remote node was offline is
-- remembered here and deployed (the branch head at that time) once the
-- node's agent reconnects. pending_push_link records the repository link
-- (full name, branch, root directory) the push was for: a link changed in
-- the meantime drops the pending push. Purely additive.
ALTER TABLE github_repos ADD COLUMN pending_push_sha TEXT
    CHECK (pending_push_sha IS NULL OR length(pending_push_sha) <= 64);
ALTER TABLE github_repos ADD COLUMN pending_push_link TEXT
    CHECK (pending_push_link IS NULL OR length(pending_push_link) <= 1024);
ALTER TABLE github_repos ADD COLUMN pending_push_at_ms INTEGER;
