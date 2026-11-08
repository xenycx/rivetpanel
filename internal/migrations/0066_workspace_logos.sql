-- Custom workspace logos, and a "New picture" choice for generated logos.
--
-- Workspaces get the same small uploaded logo as bots and sites (0037): a
-- PNG or JPEG resized by the browser, at most 256 KiB.
--
-- logo_variant picks which generated (DiceBear) picture an item shows when
-- it has no image of its own. The browser seeds the picture with the item's
-- id while the variant is 0, so existing pictures do not change, and with
-- "<id>:<variant>" after someone asks for a new picture. Every column is
-- additive with a default; existing rows are unchanged.
ALTER TABLE workspaces ADD COLUMN logo BLOB CHECK (logo IS NULL OR length(logo) <= 262144);
ALTER TABLE workspaces ADD COLUMN logo_type TEXT CHECK (logo_type IS NULL OR logo_type IN ('image/png', 'image/jpeg'));
ALTER TABLE workspaces ADD COLUMN logo_updated_at_ms INTEGER;
ALTER TABLE workspaces ADD COLUMN logo_variant INTEGER NOT NULL DEFAULT 0 CHECK (logo_variant >= 0);
ALTER TABLE bots ADD COLUMN logo_variant INTEGER NOT NULL DEFAULT 0 CHECK (logo_variant >= 0);
ALTER TABLE sites ADD COLUMN logo_variant INTEGER NOT NULL DEFAULT 0 CHECK (logo_variant >= 0);
ALTER TABLE users ADD COLUMN logo_variant INTEGER NOT NULL DEFAULT 0 CHECK (logo_variant >= 0);
