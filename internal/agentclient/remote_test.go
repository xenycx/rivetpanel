package agentclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A remote server can be torn down twice (the agent's reconciler and the
// panel's purge race after a delete). The second DeleteBotRow finds the row
// gone; that must not fail the purge and with it the user's delete, exactly
// like the panel's own SQLite store.
func TestDeleteBotRowIsIdempotent(t *testing.T) {
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/agent/v1/bots/b1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	r := &Remote{Client: func() (*http.Client, error) {
		return &http.Client{Transport: rewrite{srv.URL}}, nil
	}}
	for _, st := range []int{http.StatusNoContent, http.StatusNotFound} {
		status = st
		if err := r.DeleteBotRow(context.Background(), "b1"); err != nil {
			t.Fatalf("status %d: %v", st, err)
		}
	}
	status = http.StatusInternalServerError
	if err := r.DeleteBotRow(context.Background(), "b1"); err == nil {
		t.Fatal("a panel failure must still be reported")
	}
}

// rewrite sends the agent's "http://panel/..." requests to a test server.
type rewrite struct{ base string }

func (x rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	nu, err := u.Parse(x.base + req.URL.Path)
	if err != nil {
		return nil, err
	}
	r2 := req.Clone(req.Context())
	r2.URL, r2.Host = nu, nu.Host
	return http.DefaultTransport.RoundTrip(r2)
}
