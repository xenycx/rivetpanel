package agenthub_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"testing"

	"github.com/xenycx/rivetpanel/internal/agentproto"
)

// rotate asks the hub for a renewal over conn and returns the new identity.
func rotate(t *testing.T, conn *agentConn) (tls.Certificate, string) {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "rivet-agent"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	code, body, err := conn.call(http.MethodPost, "/agent/v1/rotate",
		agentproto.Rotate{CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))})
	if err != nil || code != http.StatusOK {
		t.Fatalf("rotate: %d %s %v", code, body, err)
	}
	var r agentproto.Rotated
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	leaf := parsePEMCert(t, []byte(r.Certificate))
	return tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}, r.Serial
}

// TestHubRevokesSupersededCertificateAfterRenewalIsUsed proves that a
// completed rotation retires the previous certificate, but only once the
// renewal has authenticated: an agent that lost its renewal before saving it
// can still reconnect with the old certificate.
func TestHubRevokesSupersededCertificateAfterRenewalIsUsed(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")
	conn, err := f.connect(t, n.cert)
	if err != nil {
		t.Fatal(err)
	}
	renewed, newSerial := rotate(t, conn)
	conn.ys.Close()
	waitFor(t, func() bool { return !f.hub.Connected(n.id) })

	// Crash window: the renewal was issued but never used. The old
	// certificate must still be accepted, and the renewal stays usable.
	if _, err := f.connect(t, n.cert); err != nil {
		t.Fatalf("old certificate refused before the renewal was used: %v", err)
	}
	if c, err := f.db.GetAgentCertificate(context.Background(), newSerial); err != nil || c.RevokedAtMS != nil {
		t.Fatalf("unused renewal = %+v, %v; want unrevoked", c, err)
	}

	// The agent reconnects with the renewal: the old serial is retired.
	if _, err := f.connect(t, renewed); err != nil {
		t.Fatalf("renewed certificate refused: %v", err)
	}
	old, err := f.db.GetAgentCertificate(context.Background(), n.serial)
	if err != nil || old.RevokedAtMS == nil || old.RevokedReason == nil || *old.RevokedReason != "superseded" {
		t.Fatalf("old certificate = %+v, %v; want revoked as superseded", old, err)
	}
	f.hub.Disconnect(n.id)
	waitFor(t, func() bool { return !f.hub.Connected(n.id) })
	f.mustBeRefused(t, "superseded certificate", n.cert, n.id)

	// The renewal itself keeps working and is the node's current one.
	if _, err := f.connect(t, renewed); err != nil {
		t.Fatalf("renewed certificate refused after retirement: %v", err)
	}
	st, err := f.db.GetAgentState(context.Background(), n.id)
	if err != nil || st.CertificateSerial == nil || *st.CertificateSerial != newSerial {
		t.Fatalf("current serial = %+v, %v; want %s", st.CertificateSerial, err, newSerial)
	}
}

// TestHubRetiresUnusedRenewalOnceANewerOneIsUsed covers two renewals where
// the first was lost: using the second retires both older certificates.
func TestHubRetiresUnusedRenewalOnceANewerOneIsUsed(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")
	conn, err := f.connect(t, n.cert)
	if err != nil {
		t.Fatal(err)
	}
	lost, _ := rotate(t, conn)
	kept, _ := rotate(t, conn)
	conn.ys.Close()
	waitFor(t, func() bool { return !f.hub.Connected(n.id) })
	if _, err := f.connect(t, kept); err != nil {
		t.Fatalf("latest renewal refused: %v", err)
	}
	f.hub.Disconnect(n.id)
	waitFor(t, func() bool { return !f.hub.Connected(n.id) })
	f.mustBeRefused(t, "original certificate", n.cert, n.id)
	f.mustBeRefused(t, "lost renewal", lost, n.id)
}
