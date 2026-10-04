// Package secrets encrypts environment values with AES-256-GCM. Keys live in
// files outside SQLite; encryption does not protect values from an operator
// with access to the host or the Docker daemon.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

var keyIDRe = lazyre.New(`^[A-Za-z0-9._-]{1,32}$`)

// Keyring holds all retained keys and the active key used for new encryption.
type Keyring struct {
	active string
	aeads  map[string]cipher.AEAD
}

// Sealed is an encrypted value.
type Sealed struct {
	Ciphertext []byte
	Nonce      []byte
	KeyID      string
}

// NewKeyring builds a keyring from raw 32-byte keys.
func NewKeyring(active string, keys map[string][]byte) (*Keyring, error) {
	k := &Keyring{active: active, aeads: map[string]cipher.AEAD{}}
	for id, key := range keys {
		if !keyIDRe.MatchString(id) {
			return nil, fmt.Errorf("invalid key id %q", id)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("key %q must be 32 bytes", id)
		}
		blk, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		g, err := cipher.NewGCM(blk)
		if err != nil {
			return nil, err
		}
		k.aeads[id] = g
	}
	if _, ok := k.aeads[active]; !ok {
		return nil, fmt.Errorf("active key %q not found in keyring", active)
	}
	return k, nil
}

// GenerateKeyFile writes a new random key to dir/<id>.key (hex, mode 0600).
// It refuses to overwrite an existing key.
func GenerateKeyFile(dir, id string) error {
	if !keyIDRe.MatchString(id) {
		return fmt.Errorf("invalid key id %q", id)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".key"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(hex.EncodeToString(raw) + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// LoadDir loads every dir/*.key file. Key files must not be group/world
// accessible. If create is true and the directory holds no keys, the active key
// is generated (intended for development only).
func LoadDir(dir, active string, create bool) (*Keyring, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.key"))
	if len(files) == 0 {
		if !create {
			return nil, fmt.Errorf("no encryption keys in %s (create one with: rivetpanel keygen)", dir)
		}
		if err := GenerateKeyFile(dir, active); err != nil {
			return nil, err
		}
		files, _ = filepath.Glob(filepath.Join(dir, "*.key"))
	}
	keys := map[string][]byte{}
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		if st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("key file %s must not be group/world accessible (chmod 600)", f)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			return nil, fmt.Errorf("key file %s: not hex", f)
		}
		keys[strings.TrimSuffix(filepath.Base(f), ".key")] = raw
	}
	return NewKeyring(active, keys)
}

// aad encodes (bot_id, name, key_id) unambiguously with length prefixes.
func aad(botID, name, keyID string) []byte {
	var out []byte
	for _, s := range []string{botID, name, keyID} {
		out = binary.BigEndian.AppendUint32(out, uint32(len(s)))
		out = append(out, s...)
	}
	return out
}

// Seal encrypts plaintext under the active key with a fresh random nonce.
func (k *Keyring) Seal(botID, name string, plaintext []byte) (Sealed, error) {
	g := k.aeads[k.active]
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, err
	}
	return Sealed{Ciphertext: g.Seal(nil, nonce, plaintext, aad(botID, name, k.active)), Nonce: nonce, KeyID: k.active}, nil
}

// Active returns the ID of the key used for new encryption.
func (k *Keyring) Active() string { return k.active }

// Reseal re-encrypts a value under the active key. It returns changed=false
// when the value already uses the active key.
func (k *Keyring) Reseal(ns, name string, s Sealed) (Sealed, bool, error) {
	if s.KeyID == k.active {
		return s, false, nil
	}
	pt, err := k.Open(ns, name, s)
	if err != nil {
		return Sealed{}, false, err
	}
	out, err := k.Seal(ns, name, pt)
	clear(pt)
	return out, err == nil, err
}

// Open decrypts a sealed value using the key it names.
func (k *Keyring) Open(botID, name string, s Sealed) ([]byte, error) {
	g, ok := k.aeads[s.KeyID]
	if !ok {
		return nil, fmt.Errorf("encryption key %q is not available", s.KeyID)
	}
	pt, err := g.Open(nil, s.Nonce, s.Ciphertext, aad(botID, name, s.KeyID))
	if err != nil {
		return nil, errors.New("decryption failed")
	}
	return pt, nil
}

// Has reports whether the keyring holds the named key.
func (k *Keyring) Has(id string) bool { _, ok := k.aeads[id]; return ok }

// Owner namespaces and names for sealed values other than bot environment
// variables. They are the AES-GCM associated data (with the key ID), so a value
// cannot be moved to another owner or purpose. Shared by the services that seal
// and the verifier that trial-decrypts.
func OAuthNS(userID string) string            { return "oauth:" + userID }
func OAuthTokenName(provider string) string   { return provider + ":token" }
func OAuthWebhookName(provider string) string { return provider + ":webhook" }
func GitHubNS(botID string) string            { return "github:" + botID }
func MFANS(userID string) string              { return "mfa:" + userID }
func AIProviderNS(providerID string) string   { return "ai-provider:" + providerID }

const AIProviderKeyName = "bearer"

// GitHubSecretName names a bot's sealed webhook secret.
const GitHubSecretName = "webhook"
