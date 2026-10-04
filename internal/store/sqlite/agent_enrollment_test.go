package sqlite_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

func openAgentTest(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background(), migrations.FS); err != nil {
		t.Fatal(err)
	}
	return db
}

func agentCSR(t *testing.T) string {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "attacker-chosen-name"}, DNSNames: []string{"attacker.invalid"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestAgentEnrollmentIsSingleUseAndPanelScoped(t *testing.T) {
	db := openAgentTest(t)
	ca, err := agentcert.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	svc := &service.AgentEnrollmentService{Store: db, CA: ca, Now: func() time.Time { return now }}
	plain, enrollment, err := svc.Create(context.Background(), "edge-one", domain.LocalLocationID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.TokenHash != nil || enrollment.Prefix == plain {
		t.Fatal("plaintext or token hash exposed in enrollment metadata")
	}
	rows, err := svc.List(context.Background())
	if err != nil || len(rows) != 1 || rows[0].TokenHash != nil || rows[0].NodeID != enrollment.NodeID {
		t.Fatalf("list = %+v, %v", rows, err)
	}
	if _, err := svc.Enroll(context.Background(), plain, "not a CSR"); err == nil {
		t.Fatal("invalid CSR accepted")
	}
	issued, err := svc.Enroll(context.Background(), plain, agentCSR(t))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(issued.Certificate))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "rivet-agent:"+enrollment.NodeID || len(cert.DNSNames) != 0 ||
		len(cert.URIs) != 1 || cert.URIs[0].String() != "spiffe://rivetpanel/node/"+enrollment.NodeID {
		t.Fatalf("panel did not replace CSR identity: %+v", cert)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("certificate is not client-only: %+v", cert.ExtKeyUsage)
	}
	if _, err := svc.Enroll(context.Background(), plain, agentCSR(t)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("single-use token reused: %v", err)
	}
	var serial string
	if err := db.QueryRowContext(context.Background(), `SELECT certificate_serial FROM node_agent_state WHERE node_id = ?`, enrollment.NodeID).Scan(&serial); err != nil || serial != issued.Serial {
		t.Fatalf("stored serial = %q, %v", serial, err)
	}
}

func TestAgentEnrollmentRevocation(t *testing.T) {
	db := openAgentTest(t)
	ca, _ := agentcert.LoadOrCreate(t.TempDir())
	svc := &service.AgentEnrollmentService{Store: db, CA: ca}
	plain, enrollment, err := svc.Create(context.Background(), "revoked-node", domain.LocalLocationID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), enrollment.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), enrollment.ID); err != nil {
		t.Fatalf("repeat revoke must be idempotent: %v", err)
	}
	if _, err := svc.Enroll(context.Background(), plain, agentCSR(t)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked token enrolled: %v", err)
	}
}

func TestAgentEnrollmentFailedIssueLeavesTokenUsable(t *testing.T) {
	db := openAgentTest(t)
	ca, err := agentcert.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := &service.AgentEnrollmentService{Store: db, CA: ca}
	plain, enrollment, err := svc.Create(context.Background(), "flaky-signer", domain.LocalLocationID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	signerDown := errors.New("signer unavailable")
	_, err = db.EnrollAgent(context.Background(), auth.HashToken(plain), time.Now().UnixMilli(),
		func(string) (string, int64, error) { return "", 0, signerDown })
	if !errors.Is(err, signerDown) {
		t.Fatalf("err = %v", err)
	}
	var used *int64
	var serial *string
	if err := db.QueryRowContext(context.Background(), `SELECT t.used_at_ms, s.certificate_serial
		FROM agent_enrollment_tokens t JOIN node_agent_state s ON s.node_id = t.node_id
		WHERE t.id = ?`, enrollment.ID).Scan(&used, &serial); err != nil {
		t.Fatal(err)
	}
	if used != nil || serial != nil {
		t.Fatalf("failed issue consumed token or recorded cert: used=%v serial=%v", used, serial)
	}
	if _, err := svc.Enroll(context.Background(), plain, agentCSR(t)); err != nil {
		t.Fatalf("token unusable after failed issue: %v", err)
	}
}

func TestAgentEnrollmentReissueAndDiscard(t *testing.T) {
	db := openAgentTest(t)
	if err := db.EnsureLocalNode(context.Background()); err != nil {
		t.Fatal(err)
	}
	ca, _ := agentcert.LoadOrCreate(t.TempDir())
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	svc := &service.AgentEnrollmentService{Store: db, CA: ca, Now: func() time.Time { return now }}
	ctx := context.Background()
	old, enrollment, err := svc.Create(ctx, "late-node", domain.LocalLocationID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fresh, reissued, err := svc.Reissue(ctx, enrollment.NodeID, time.Hour)
	if err != nil || reissued.NodeID != enrollment.NodeID || reissued.ID == enrollment.ID || reissued.TokenHash != nil {
		t.Fatalf("reissue = %+v, %v", reissued, err)
	}
	if rows, _ := svc.List(ctx); len(rows) != 1 || rows[0].ID != reissued.ID {
		t.Fatalf("old token not replaced: %+v", rows)
	}
	if _, err := svc.Enroll(ctx, old, agentCSR(t)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("replaced token enrolled: %v", err)
	}
	if _, err := svc.Enroll(ctx, fresh, agentCSR(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Reissue(ctx, enrollment.NodeID, time.Hour); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("reissue for an enrolled node: %v", err)
	}
	if err := svc.Discard(ctx, enrollment.NodeID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("discard of an enrolled node: %v", err)
	}
	if _, _, err := svc.Reissue(ctx, domain.LocalNodeID, time.Hour); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reissue for the local node: %v", err)
	}
	if err := svc.Discard(ctx, domain.LocalNodeID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("discard of the local node: %v", err)
	}

	_, placeholder, err := svc.Create(ctx, "never-enrolled", domain.LocalLocationID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Discard(ctx, placeholder.NodeID); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM nodes WHERE id = ?) +
		(SELECT count(*) FROM agent_enrollment_tokens WHERE node_id = ?) +
		(SELECT count(*) FROM node_agent_state WHERE node_id = ?)`,
		placeholder.NodeID, placeholder.NodeID, placeholder.NodeID).Scan(&n)
	if n != 0 {
		t.Fatalf("placeholder rows left behind: %d", n)
	}
	if err := svc.Discard(ctx, placeholder.NodeID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("repeat discard: %v", err)
	}
}
