package backup

import (
	"context"
	"sort"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// EnvReport summarizes whether stored environment values can be decrypted.
type EnvReport struct {
	Total       int
	OK          int
	MissingKeys []string // key IDs referenced by rows but absent from the keyring
	Failed      int      // rows that fail authentication (wrong key material or tampering)
}

// Healthy is true when every value decrypts.
func (r EnvReport) Healthy() bool { return r.OK == r.Total }

// VerifyEnv trial-decrypts every sealed value (environment variables, OAuth
// tokens, webhooks) without exposing plaintext.
func VerifyEnv(ctx context.Context, db *sqlite.DB, keys *secrets.Keyring) (EnvReport, error) {
	var r EnvReport
	missing := map[string]bool{}
	err := db.WalkEnv(ctx, func(e domain.EnvVar) error {
		r.Total++
		if !keys.Has(e.KeyID) {
			missing[e.KeyID] = true
			return nil
		}
		if _, err := keys.Open(e.BotID, e.Name, secrets.Sealed{Ciphertext: e.Ciphertext, Nonce: e.Nonce, KeyID: e.KeyID}); err != nil {
			r.Failed++
			return nil
		}
		r.OK++
		return nil
	})
	if err != nil {
		return r, err
	}
	// OAuth tokens, Discord webhooks and GitHub webhook secrets are sealed with the same keys.
	err = db.WalkSealed(ctx, func(e sqlite.SealedRow) error {
		r.Total++
		if !keys.Has(e.KeyID) {
			missing[e.KeyID] = true
			return nil
		}
		if _, err := keys.Open(e.NS, e.Name, secrets.Sealed{Ciphertext: e.Ciphertext, Nonce: e.Nonce, KeyID: e.KeyID}); err != nil {
			r.Failed++
			return nil
		}
		r.OK++
		return nil
	})
	for id := range missing {
		r.MissingKeys = append(r.MissingKeys, id)
	}
	sort.Strings(r.MissingKeys)
	return r, err
}
