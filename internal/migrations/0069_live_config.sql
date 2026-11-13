-- Configuration and environment edits no longer need a stopped bot. A save
-- on a live bot keeps its generation (so the running container is not
-- replaced) and records here when it happened and at which generation.
-- The bot shows "restart to apply" while config_pending_at_ms > 0 and
-- config_pending_generation still equals its generation; the next start or
-- restart advances the generation and applies the change. Purely additive:
-- existing rows read as "nothing pending" (0, 0).
ALTER TABLE bots ADD COLUMN config_pending_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bots ADD COLUMN config_pending_generation INTEGER NOT NULL DEFAULT 0;
