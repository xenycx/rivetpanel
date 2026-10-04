-- Task-chain schedules and the installed game version. Purely additive.
--
-- A schedule with chain = 1 runs its schedule_tasks in order instead of its
-- single action (its action column then holds a placeholder that is never
-- executed). Each task waits delay_seconds before it runs; a failed task stops
-- the chain unless continue_on_failure is set.
ALTER TABLE schedules ADD COLUMN chain INTEGER NOT NULL DEFAULT 0
    CHECK (chain IN (0, 1));

CREATE TABLE schedule_tasks (
    schedule_id         TEXT NOT NULL
                        REFERENCES schedules(id) ON DELETE CASCADE,
    seq                 INTEGER NOT NULL CHECK (seq BETWEEN 0 AND 19),
    action              TEXT NOT NULL
                        CHECK (action IN ('command', 'start', 'stop', 'restart', 'kill', 'backup')),
    payload             TEXT NOT NULL DEFAULT ''
                        CHECK (length(payload) <= 1000),
    delay_seconds       INTEGER NOT NULL DEFAULT 0
                        CHECK (delay_seconds BETWEEN 0 AND 3600),
    continue_on_failure INTEGER NOT NULL DEFAULT 0
                        CHECK (continue_on_failure IN (0, 1)),
    PRIMARY KEY (schedule_id, seq)
) STRICT, WITHOUT ROWID;

-- The version a game server's last installation actually resolved ("latest"
-- becomes a concrete version), used to offer compatible mods and plugins.
ALTER TABLE bots ADD COLUMN installed_version TEXT NOT NULL DEFAULT ''
    CHECK (length(installed_version) <= 64);
