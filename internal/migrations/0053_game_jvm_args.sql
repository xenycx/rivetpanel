-- Extra JVM options for Java game servers. Purely additive: existing rows
-- read as "none set" ('' and 0). The panel validates the options before they
-- are stored and again before each start. jvm_args_generation is the
-- server's generation when they were saved: a server still running that
-- generation's container shows "Restart to apply".
ALTER TABLE bots ADD COLUMN jvm_args TEXT NOT NULL DEFAULT '';
ALTER TABLE bots ADD COLUMN jvm_args_updated_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bots ADD COLUMN jvm_args_generation INTEGER NOT NULL DEFAULT 0;
