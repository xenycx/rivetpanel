package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// virtualAuthenticator is a minimal software passkey (ES256, "none"
// attestation, user present and verified) for exercising the server side.
type virtualAuthenticator struct {
	t      *testing.T
	origin string
	rpID   string
	key    *ecdsa.PrivateKey
	credID []byte
	user   []byte // user handle given at registration
	count  uint32
	uv     bool
}

var b64u = base64.RawURLEncoding

func newVirtualAuthenticator(t *testing.T) *virtualAuthenticator {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &virtualAuthenticator{t: t, origin: "https://panel.test", rpID: "panel.test", key: k, credID: id, uv: true}
}

func (v *virtualAuthenticator) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": v.origin, "crossOrigin": false})
	return b
}

func (v *virtualAuthenticator) authData(attested bool) []byte {
	rp := sha256.Sum256([]byte(v.rpID))
	flags := byte(0x01) // user present
	if v.uv {
		flags |= 0x04
	}
	if attested {
		flags |= 0x40
	}
	v.count++
	out := append(rp[:], flags)
	out = binary.BigEndian.AppendUint32(out, v.count)
	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(v.credID)))
		out = append(out, v.credID...)
		x, y := make([]byte, 32), make([]byte, 32)
		v.key.X.FillBytes(x)
		v.key.Y.FillBytes(y)
		cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		out = append(out, cose...)
	}
	return out
}

// create answers navigator.credentials.create() options.
func (v *virtualAuthenticator) create(options json.RawMessage) json.RawMessage {
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		v.t.Fatal(err)
	}
	v.user, _ = b64u.DecodeString(o.PublicKey.User.ID)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": v.authData(true)})
	b, _ := json.Marshal(map[string]any{"id": b64u.EncodeToString(v.credID), "rawId": b64u.EncodeToString(v.credID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64u.EncodeToString(v.clientData("webauthn.create", o.PublicKey.Challenge)),
			"attestationObject": b64u.EncodeToString(att), "transports": []string{"internal"}}})
	return b
}

// get answers navigator.credentials.get() options.
func (v *virtualAuthenticator) get(options json.RawMessage) json.RawMessage {
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		v.t.Fatal(err)
	}
	cd := v.clientData("webauthn.get", o.PublicKey.Challenge)
	ad := v.authData(false)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, v.key, digest[:])
	if err != nil {
		v.t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"id": b64u.EncodeToString(v.credID), "rawId": b64u.EncodeToString(v.credID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64u.EncodeToString(cd), "authenticatorData": b64u.EncodeToString(ad),
			"signature": b64u.EncodeToString(sig), "userHandle": b64u.EncodeToString(v.user)}})
	return b
}

func passkeyEnv(t *testing.T) *env {
	e := newEnv(t)
	mfa := &service.MFAService{Store: e.db, Keys: e.bots.Keys, Auth: e.auth}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, SecureCookies: true, MFA: mfa,
		Passkeys: &service.PasskeyService{Store: e.db, Auth: e.auth, MFA: mfa, PublicURL: func() string { return "https://panel.test" }}})
	return e
}

type ceremonyOut struct {
	Ticket  string          `json:"ticket"`
	Options json.RawMessage `json:"options"`
}

func (c *client) registerPasskey(t *testing.T, v *virtualAuthenticator, name string) passkeyDTO {
	t.Helper()
	var b ceremonyOut
	json.Unmarshal(c.mustStatus(200, "POST", "/api/v1/me/passkeys/register/begin", map[string]any{"password": pw}), &b)
	var k passkeyDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/me/passkeys/register/finish", map[string]any{"ticket": b.Ticket, "name": name, "credential": v.create(b.Options)}), &k)
	return k
}

// passkeyLogin runs a passwordless sign-in from a fresh browser.
func passkeyLogin(e *env, v *virtualAuthenticator) (*http.Response, []byte) {
	anon := &client{e: e}
	_, body := anon.do("POST", "/api/v1/auth/passkey/begin", nil)
	var b ceremonyOut
	json.Unmarshal(body, &b)
	return anon.do("POST", "/api/v1/auth/passkey/finish", map[string]any{"ticket": b.Ticket, "credential": v.get(b.Options)})
}

func TestPasskeys(t *testing.T) {
	e := passkeyEnv(t)
	alice := e.user("alice@x.io", domain.RoleUser)
	v := newVirtualAuthenticator(t)

	// Registration needs the current password.
	alice.mustStatus(400, "POST", "/api/v1/me/passkeys/register/begin", map[string]any{"password": "wrong-password-here"})
	k := alice.registerPasskey(t, v, "Laptop")
	var l struct {
		Passkeys  []passkeyDTO
		Available bool
	}
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/me/passkeys", nil), &l)
	if len(l.Passkeys) != 1 || l.Passkeys[0].Name != "Laptop" || !l.Available {
		t.Fatalf("list: %+v", l)
	}
	// The same authenticator cannot be registered twice.
	var b ceremonyOut
	json.Unmarshal(alice.mustStatus(200, "POST", "/api/v1/me/passkeys/register/begin", map[string]any{"password": pw}), &b)
	alice.mustStatus(400, "POST", "/api/v1/me/passkeys/register/finish", map[string]any{"ticket": b.Ticket, "name": "Again", "credential": v.create(b.Options)})
	v.count = 1

	// Passwordless sign-in.
	resp, body := passkeyLogin(e, v)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "alice@x.io") {
		t.Fatalf("passkey sign-in: %d %s", resp.StatusCode, body)
	}
	var signedIn bool
	for _, ck := range resp.Cookies() {
		signedIn = signedIn || (ck.Name == sessionCookie && ck.Value != "")
	}
	if !signedIn {
		t.Fatal("no session cookie")
	}
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/me/passkeys", nil), &l)
	if l.Passkeys[0].LastUsedAtMS == nil {
		t.Fatal("last use not recorded")
	}

	// A replayed ceremony ticket, a wrong signature, a counter going back
	// (a cloned authenticator) and a missing user verification are refused.
	anon := &client{e: e}
	_, body = anon.do("POST", "/api/v1/auth/passkey/begin", nil)
	json.Unmarshal(body, &b)
	good := v.get(b.Options)
	anon.mustStatus(200, "POST", "/api/v1/auth/passkey/finish", map[string]any{"ticket": b.Ticket, "credential": good})
	anon.mustStatus(400, "POST", "/api/v1/auth/passkey/finish", map[string]any{"ticket": b.Ticket, "credential": good})

	other := newVirtualAuthenticator(t)
	other.credID, other.user, other.count = v.credID, v.user, v.count // same id, different key
	if resp, _ := passkeyLogin(e, other); resp.StatusCode != 400 {
		t.Fatalf("wrong key: %d", resp.StatusCode)
	}
	saved := v.count
	v.count = 0
	if resp, _ := passkeyLogin(e, v); resp.StatusCode != 400 {
		t.Fatalf("counter went back: %d", resp.StatusCode)
	}
	v.count = saved + 10
	v.uv = false
	if resp, _ := passkeyLogin(e, v); resp.StatusCode != 400 {
		t.Fatalf("no user verification: %d", resp.StatusCode)
	}
	v.uv = true
	// An unknown credential.
	stranger := newVirtualAuthenticator(t)
	stranger.user = v.user
	if resp, _ := passkeyLogin(e, stranger); resp.StatusCode != 400 {
		t.Fatalf("unknown credential: %d", resp.StatusCode)
	}

	// Rename and delete; another account cannot touch it.
	bob := e.user("bob@x.io", domain.RoleUser)
	bob.mustStatus(404, "PATCH", "/api/v1/me/passkeys/"+k.ID, map[string]any{"name": "Mine"})
	bob.mustStatus(404, "DELETE", "/api/v1/me/passkeys/"+k.ID, nil)
	alice.mustStatus(204, "PATCH", "/api/v1/me/passkeys/"+k.ID, map[string]any{"name": "Work laptop"})
	alice.mustStatus(204, "DELETE", "/api/v1/me/passkeys/"+k.ID, nil)
	if resp, _ := passkeyLogin(e, v); resp.StatusCode != 400 {
		t.Fatalf("deleted passkey: %d", resp.StatusCode)
	}
}

// With two-step sign-in on, a passkey can replace the code, but only one of
// the account that passed the first step.
func TestPasskeyAsSecondFactor(t *testing.T) {
	e := passkeyEnv(t)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	va, vb := newVirtualAuthenticator(t), newVirtualAuthenticator(t)
	alice.registerPasskey(t, va, "Key")
	bob.registerPasskey(t, vb, "Key")
	// Turn on TOTP for alice.
	var setup struct{ Key string }
	json.Unmarshal(alice.mustStatus(200, "POST", "/api/v1/me/mfa/setup", map[string]any{"password": pw}), &setup)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Key)
	if err != nil {
		t.Fatal(err)
	}
	code := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	alice.mustStatus(200, "POST", "/api/v1/me/mfa/enable", map[string]any{"code": code})

	login := func() string {
		c := &client{e: e}
		resp, body := c.do("POST", "/api/v1/auth/login", map[string]string{"email": "alice@x.io", "password": pw})
		if resp.StatusCode != 200 || !strings.Contains(string(body), "mfa_required") {
			t.Fatalf("first step: %d %s", resp.StatusCode, body)
		}
		for _, ck := range resp.Cookies() {
			if ck.Name == mfaCookie {
				return ck.Value
			}
		}
		t.Fatal("no MFA cookie")
		return ""
	}
	step := func(ticket string, v *virtualAuthenticator) int {
		c := &client{e: e}
		with := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: mfaCookie, Value: ticket}) }
		_, body := c.req("POST", "/api/v1/auth/mfa/passkey/begin", nil, with)
		var b ceremonyOut
		json.Unmarshal(body, &b)
		resp, _ := c.req("POST", "/api/v1/auth/mfa/passkey/finish", map[string]any{"ticket": b.Ticket, "credential": v.get(b.Options)}, with)
		return resp.StatusCode
	}
	if got := step(login(), vb); got != 400 { // bob's passkey for alice's sign-in
		t.Fatalf("another account's passkey: %d", got)
	}
	if got := step(login(), va); got != 200 {
		t.Fatalf("passkey second step: %d", got)
	}
	// Passwordless passkey sign-in does not ask for the code.
	if resp, _ := passkeyLogin(e, va); resp.StatusCode != 200 {
		t.Fatalf("passwordless with MFA on: %d", resp.StatusCode)
	}
}
