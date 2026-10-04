-- Passkeys (WebAuthn credentials). Purely additive.
--
-- credential_json is the go-webauthn credential record (public key, sign
-- counter, flags, transports, attestation format); it holds no secret: the
-- private key never leaves the authenticator. credential_id is the
-- authenticator's credential id, unique across the panel.
CREATE TABLE webauthn_credentials (
    id               TEXT PRIMARY KEY NOT NULL,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id    BLOB NOT NULL UNIQUE CHECK (length(credential_id) BETWEEN 1 AND 1023),
    name             TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 64),
    credential_json  TEXT NOT NULL CHECK (json_valid(credential_json) AND length(credential_json) <= 65536),
    created_at_ms    INTEGER NOT NULL,
    last_used_at_ms  INTEGER
) STRICT;
CREATE INDEX webauthn_credentials_user_idx ON webauthn_credentials(user_id);
