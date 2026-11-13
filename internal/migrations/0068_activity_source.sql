-- Activity: who or what performed a recorded action. Existing rows were all
-- performed by people (the API records them), so they default to 'person'.
-- 'schedule' marks a scheduled action the panel ran on its owner's behalf,
-- 'system' something the panel itself observed or did (a crash, an automatic
-- restart) and 'ai' a live action the AI operator took for someone.
ALTER TABLE audit_events ADD COLUMN source TEXT NOT NULL DEFAULT 'person'
    CHECK (source IN ('person', 'schedule', 'system', 'ai'));

UPDATE audit_events SET source = 'ai'
    WHERE action IN ('ai.file_apply', 'ai.diagnostic', 'ai.restart', 'ai.env_input');

CREATE INDEX audit_source_idx ON audit_events(source, at_ms);
