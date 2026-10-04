package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const passkeyCols = `id, user_id, credential_id, name, credential_json, created_at_ms, last_used_at_ms`

func scanPasskey(row interface{ Scan(...any) error }) (domain.Passkey, error) {
	var p domain.Passkey
	var js string
	if err := row.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.Name, &js, &p.CreatedAtMS, &p.LastUsedAtMS); err != nil {
		return p, mapErr(err)
	}
	p.Credential = []byte(js)
	return p, nil
}

// InsertPasskey stores a credential; a credential id already registered is
// ErrConflict.
func (db *DB) InsertPasskey(ctx context.Context, p domain.Passkey) error {
	_, err := db.ExecContext(ctx, `INSERT INTO webauthn_credentials (`+passkeyCols+`) VALUES (?,?,?,?,?,?,?)`,
		p.ID, p.UserID, p.CredentialID, p.Name, string(p.Credential), p.CreatedAtMS, p.LastUsedAtMS)
	return mapErr(err)
}

// ListPasskeys returns an account's passkeys, oldest first.
func (db *DB) ListPasskeys(ctx context.Context, userID string) ([]domain.Passkey, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+passkeyCols+` FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at_ms LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPasskeyByCredentialID resolves a credential presented at sign-in.
func (db *DB) GetPasskeyByCredentialID(ctx context.Context, credID []byte) (domain.Passkey, error) {
	return scanPasskey(db.QueryRowContext(ctx, `SELECT `+passkeyCols+` FROM webauthn_credentials WHERE credential_id = ?`, credID))
}

// UpdatePasskeyUse stores the credential record after a sign-in (sign
// counter, flags) and the time.
func (db *DB) UpdatePasskeyUse(ctx context.Context, id string, credential []byte, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE webauthn_credentials SET credential_json = ?, last_used_at_ms = ? WHERE id = ?`, string(credential), nowMS, id)
	return err
}

// RenamePasskey renames one of an account's passkeys.
func (db *DB) RenamePasskey(ctx context.Context, userID, id, name string) error {
	res, err := db.ExecContext(ctx, `UPDATE webauthn_credentials SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeletePasskey removes one of an account's passkeys.
func (db *DB) DeletePasskey(ctx context.Context, userID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountPasskeys counts an account's passkeys (sign-in methods).
func (db *DB) CountPasskeys(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM webauthn_credentials WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}
