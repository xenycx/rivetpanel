package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func enrollCSR(t *testing.T) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func postEnroll(t *testing.T, app *fiber.App, contentType, body string) (*http.Response, []byte) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/agent/v1/enroll", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	resp, err := app.Test(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func enrollBody(token, csr string) string {
	b, _ := json.Marshal(map[string]string{"token": token, "csr": csr})
	return string(b)
}

func TestAgentEnrollHTTPContract(t *testing.T) {
	e := newEnv(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Without the agents module the route does not exist.
	if resp, _ := postEnroll(t, e.app, "application/json", enrollBody("rvt_enroll_x", "x")); resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("disabled enrollment: %d", resp.StatusCode)
	}

	ca, err := agentcert.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := &service.AgentEnrollmentService{Store: e.db, CA: ca}
	app := New(Deps{Log: log, DB: e.db, Auth: e.auth, Bots: e.bots, Nodes: e.db, Enrollment: svc})
	plain, enrollment, err := svc.Create(context.Background(), "http-node", domain.LocalLocationID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, contentType, body string
		want                    int
	}{
		{"form body", "application/x-www-form-urlencoded", "token=" + plain, fiber.StatusUnsupportedMediaType},
		{"unknown field", "application/json", `{"token":"` + plain + `","csr":"x","node_id":"evil"}`, fiber.StatusBadRequest},
		{"wrong token", "application/json", enrollBody("rvt_enroll_wrong", enrollCSR(t)), fiber.StatusUnauthorized},
		{"invalid csr", "application/json", enrollBody(plain, "not a csr"), fiber.StatusBadRequest},
	}
	for _, tc := range cases {
		if resp, b := postEnroll(t, app, tc.contentType, tc.body); resp.StatusCode != tc.want {
			t.Fatalf("%s: got %d want %d: %s", tc.name, resp.StatusCode, tc.want, b)
		} else if strings.Contains(string(b), plain) {
			t.Fatalf("%s: response echoed the token", tc.name)
		}
	}

	// The failures above did not consume the token; no session or CSRF is needed.
	resp, b := postEnroll(t, app, "application/json", enrollBody(plain, enrollCSR(t)))
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("enroll: %d %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "agent_address,ca,certificate,expires_at_ms,node_id,serial" {
		t.Fatalf("response fields = %v", keys)
	}
	if out["node_id"] != enrollment.NodeID || !strings.Contains(out["certificate"].(string), "BEGIN CERTIFICATE") ||
		out["ca"] != string(ca.CertificatePEM()) {
		t.Fatalf("response = %s", b)
	}

	if resp, _ := postEnroll(t, app, "application/json", enrollBody(plain, enrollCSR(t))); resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("reused token: %d", resp.StatusCode)
	}

	// Ten attempts per minute per client address.
	limited := false
	for i := 0; i < 10 && !limited; i++ {
		resp, _ := postEnroll(t, app, "application/json", enrollBody("rvt_enroll_guess", enrollCSR(t)))
		limited = resp.StatusCode == fiber.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("enrollment attempts are not rate limited")
	}
}
