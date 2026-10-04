CREATE TABLE rivetpanel_identity (
    singleton       INTEGER PRIMARY KEY NOT NULL DEFAULT 1
                    CHECK (singleton = 1),
    product         TEXT NOT NULL CHECK (product = 'rivetpanel'),
    schema_family   INTEGER NOT NULL CHECK (schema_family = 1),
    created_at_ms   INTEGER NOT NULL
) STRICT;

INSERT INTO rivetpanel_identity (singleton, product, schema_family, created_at_ms)
VALUES (1, 'rivetpanel', 1, CAST(unixepoch('subsec') * 1000 AS INTEGER));
