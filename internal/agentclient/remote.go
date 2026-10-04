// Package agentclient is the rivet-agent end of the panel connection: it
// implements the runner's persistence, secret and build-recording interfaces
// by calling the panel, and keeps the connection alive.
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// ErrOffline means there is no connection to the panel right now.
var ErrOffline = errors.New("not connected to the panel")

// Remote calls the panel over the current connection.
type Remote struct {
	// Client returns the HTTP client of the current connection.
	Client func() (*http.Client, error)
}

func (r *Remote) do(ctx context.Context, method, path string, in, out any) error {
	c, err := r.Client()
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://panel"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return domain.ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%w: %s", domain.ErrConflict, bytes.TrimSpace(msg))
	case resp.StatusCode >= 300:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("panel answered %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
	}
	return nil
}

// --- runner.Store ---

func (r *Remote) GetBot(ctx context.Context, id string) (domain.Bot, error) {
	var b domain.Bot
	err := r.do(ctx, http.MethodGet, "/agent/v1/bots/"+url.PathEscape(id), nil, &b)
	return b, err
}

// ListBotsByNode returns the calling node's servers; the node argument is
// ignored because the panel derives the node from the certificate.
func (r *Remote) ListBotsByNode(ctx context.Context, _ string) ([]domain.Bot, error) {
	var out []domain.Bot
	err := r.do(ctx, http.MethodGet, "/agent/v1/bots", nil, &out)
	return out, err
}

func (r *Remote) Observe(ctx context.Context, o sqlite.Observation) (bool, error) {
	var res agentproto.ObserveResult
	err := r.do(ctx, http.MethodPost, "/agent/v1/observe", o, &res)
	return res.Updated, err
}

func (r *Remote) MarkNodeObservedUnknown(ctx context.Context, _ string, _ int64) (int64, error) {
	var res struct {
		Count int64 `json:"count"`
	}
	err := r.do(ctx, http.MethodPost, "/agent/v1/node/unknown", struct{}{}, &res)
	return res.Count, err
}

// DeleteBotRow is idempotent like the panel's own store: a row that is already
// gone (the reconciler and an explicit purge can both tear a server down) is
// success, not an error that fails the user's delete.
func (r *Remote) DeleteBotRow(ctx context.Context, id string) error {
	err := r.do(ctx, http.MethodDelete, "/agent/v1/bots/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}

func (r *Remote) TouchNode(ctx context.Context, _ string, _ int64) error {
	return r.do(ctx, http.MethodPost, "/agent/v1/node/touch", struct{}{}, nil)
}

// --- runner.GameStore ---

func (r *Remote) GetBlueprintRevision(ctx context.Context, id string, rev int64) (domain.BlueprintRevision, error) {
	var out domain.BlueprintRevision
	err := r.do(ctx, http.MethodGet, "/agent/v1/blueprints/"+url.PathEscape(id)+"/"+strconv.FormatInt(rev, 10), nil, &out)
	return out, err
}

func (r *Remote) SetInstallState(ctx context.Context, botID, state string, imageChoice *string, _ int64) error {
	return r.do(ctx, http.MethodPost, "/agent/v1/bots/"+url.PathEscape(botID)+"/install-state",
		agentproto.InstallState{State: state, ImageChoice: imageChoice}, nil)
}

func (r *Remote) SetInstalledVersion(ctx context.Context, botID, version string) error {
	return r.do(ctx, http.MethodPost, "/agent/v1/bots/"+url.PathEscape(botID)+"/installed-version",
		agentproto.InstalledVersion{Version: version}, nil)
}

// --- runner.EnvSource ---

func (r *Remote) DecryptEnv(ctx context.Context, botID string) (map[string]string, error) {
	out := map[string]string{}
	err := r.do(ctx, http.MethodGet, "/agent/v1/bots/"+url.PathEscape(botID)+"/env", nil, &out)
	return out, err
}

// --- runner.BuildRecorder ---

func bg() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func (r *Remote) BuildStarted(ctx context.Context, botID string, generation int64) string {
	var out agentproto.BuildStarted
	if err := r.do(ctx, http.MethodPost, "/agent/v1/builds", agentproto.BuildStart{BotID: botID, Generation: generation}, &out); err != nil {
		return ""
	}
	return out.ID
}

func (r *Remote) BuildStage(ctx context.Context, id, stage string) {
	if id == "" {
		return
	}
	_ = r.do(ctx, http.MethodPost, "/agent/v1/builds/"+url.PathEscape(id)+"/stage", agentproto.BuildStage{Stage: stage}, nil)
}

// BuildOutput streams output to the panel; writes never block the build for
// long because the request body is a pipe drained by the HTTP client.
func (r *Remote) BuildOutput(id string) io.WriteCloser {
	if id == "" {
		return nil
	}
	c, err := r.Client()
	if err != nil {
		return nil
	}
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, err := http.NewRequest(http.MethodPost, "http://panel/agent/v1/builds/"+url.PathEscape(id)+"/output", pr)
		if err != nil {
			pr.CloseWithError(err)
			return
		}
		resp, err := c.Do(req)
		if err != nil {
			pr.CloseWithError(err)
			return
		}
		resp.Body.Close()
		pr.Close()
	}()
	return &streamWriter{pw: pw, done: done}
}

type streamWriter struct {
	pw   *io.PipeWriter
	done chan struct{}
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if _, err := w.pw.Write(p); err != nil {
		return len(p), nil // output is best effort; never fail the build
	}
	return len(p), nil
}

func (w *streamWriter) Close() error {
	w.pw.Close()
	select {
	case <-w.done:
	case <-time.After(10 * time.Second):
	}
	return nil
}

func (r *Remote) BuildFinished(_ context.Context, id, status, code, msg string) {
	if id == "" {
		return
	}
	ctx, cancel := bg()
	defer cancel()
	_ = r.do(ctx, http.MethodPost, "/agent/v1/builds/"+url.PathEscape(id)+"/finish", agentproto.BuildFinish{Status: status, Code: code, Msg: msg}, nil)
}

// Hello announces the agent and returns the panel's welcome.
func (r *Remote) Hello(ctx context.Context, h agentproto.Hello) (agentproto.Welcome, error) {
	var w agentproto.Welcome
	err := r.do(ctx, http.MethodPost, "/agent/v1/hello", h, &w)
	return w, err
}

// Rotate exchanges a CSR for a renewed certificate.
func (r *Remote) Rotate(ctx context.Context, csrPEM string) (agentproto.Rotated, error) {
	var out agentproto.Rotated
	err := r.do(ctx, http.MethodPost, "/agent/v1/rotate", agentproto.Rotate{CSR: csrPEM}, &out)
	return out, err
}
