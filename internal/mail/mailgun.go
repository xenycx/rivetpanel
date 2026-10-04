// Package mail sends transactional email through the Mailgun HTTP API.
//
// It uses only the standard library and keeps nothing alive between messages
// (no connection pool, no queue, no goroutines), so an idle panel pays no
// memory for it. Sending is rare and small; each message is one HTTPS request.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// Regions Mailgun hosts accounts in. An account lives in exactly one, and its
// domains are only reachable through that region's API host.
const (
	RegionUS = "us"
	RegionEU = "eu"
)

// Limits that keep a message (and the request that carries it) small.
const (
	maxSubject  = 200
	maxBodyText = 64 << 10
	maxBodyHTML = 128 << 10
	maxResponse = 64 << 10
)

// Config is everything needed to send through one Mailgun domain.
type Config struct {
	APIKey string // private API key or a domain sending key
	Domain string // Mailgun sending domain, e.g. mg.example.com
	Region string // RegionUS (default) or RegionEU
	From   string // "Name <addr@domain>" or a bare address
}

// Configured reports whether every field needed to send is present.
func (c Config) Configured() bool {
	return c.APIKey != "" && c.Domain != "" && c.From != ""
}

// BaseURL is the API origin for the configured region.
func (c Config) BaseURL() string {
	if strings.EqualFold(c.Region, RegionEU) {
		return "https://api.eu.mailgun.net"
	}
	return "https://api.mailgun.net"
}

// Message is one outgoing email.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string // optional; Text is always sent as the plain alternative
	Tag     string // Mailgun o:tag, for grouping in its dashboard
	// TestMode asks Mailgun to accept the message without delivering it.
	TestMode bool
}

// Error is a failure reported by Mailgun, reduced to a status code and a
// sentence an administrator can act on. It never contains the API key.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Client calls the Mailgun API. The zero value is usable.
type Client struct {
	// BaseURL overrides the region host (tests).
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// No keep-alive: a pooled idle connection would hold a goroutine and
	// buffers for a request that happens a few times a day.
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives:     true,
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
			TLSHandshakeTimeout:   8 * time.Second,
			ResponseHeaderTimeout: 12 * time.Second,
		},
	}
}

func (c *Client) base(cfg Config) string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return cfg.BaseURL()
}

// ValidateAddress parses one mailbox and returns the bare address. It rejects
// anything with line breaks so a recipient can never smuggle in a header.
func ValidateAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("enter a single email address")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address == "" || !strings.Contains(a.Address, "@") {
		return "", errors.New("that email address does not look right")
	}
	return a.Address, nil
}

// ValidateFrom checks the sender: a bare address or "Name <address>".
func ValidateFrom(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if strings.ContainsAny(s, "\r\n\x00") || len(s) > 200 {
		return "", errors.New("the sender must be one line, like RivetPanel <noreply@example.com>")
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return "", errors.New("the sender must look like RivetPanel <noreply@example.com>")
	}
	return s, nil
}

// ValidateDomain accepts a bare host name.
func ValidateDomain(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	if len(s) > 253 || !strings.Contains(s, ".") || strings.ContainsAny(s, "/@:? #\\\r\n") {
		return "", errors.New("the Mailgun domain must be a bare host name like mg.example.com")
	}
	return s, nil
}

// MaxBatch is how many recipients go in one Mailgun request.
const MaxBatch = 100

// SendBatch delivers one message to up to MaxBatch recipients in a single
// request. Mailgun's recipient variables make every recipient see only their
// own address in To, so nobody learns who else received it.
func (c *Client) SendBatch(ctx context.Context, cfg Config, to []string, m Message) (string, error) {
	if !cfg.Configured() {
		return "", errors.New("email is not configured")
	}
	if len(to) == 0 || len(to) > MaxBatch {
		return "", fmt.Errorf("a batch holds 1 to %d recipients", MaxBatch)
	}
	vars := make(map[string]map[string]string, len(to))
	clean := make([]string, 0, len(to))
	for _, a := range to {
		addr, err := ValidateAddress(a)
		if err != nil {
			return "", fmt.Errorf("%s: %w", a, err)
		}
		clean = append(clean, addr)
		vars[addr] = map[string]string{}
	}
	rv, _ := json.Marshal(vars)
	m.To = ""
	return c.send(ctx, cfg, m, [][2]string{{"to", strings.Join(clean, ",")}, {"recipient-variables", string(rv)}})
}

// Send delivers one message and returns Mailgun's message id.
func (c *Client) Send(ctx context.Context, cfg Config, m Message) (string, error) {
	if !cfg.Configured() {
		return "", errors.New("email is not configured")
	}
	to, err := ValidateAddress(m.To)
	if err != nil {
		return "", err
	}
	return c.send(ctx, cfg, m, [][2]string{{"to", to}})
}

func (c *Client) send(ctx context.Context, cfg Config, m Message, recipients [][2]string) (string, error) {
	subject := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, m.Subject)
	if subject == "" || len([]rune(subject)) > maxSubject {
		return "", errors.New("the subject must be 1 to 200 characters")
	}
	if m.Text == "" || len(m.Text) > maxBodyText || len(m.HTML) > maxBodyHTML {
		return "", errors.New("the message body is empty or too large")
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fields := append([][2]string{{"from", cfg.From}}, recipients...)
	fields = append(fields, [2]string{"subject", subject}, [2]string{"text", m.Text})
	if m.HTML != "" {
		fields = append(fields, [2]string{"html", m.HTML})
	}
	if m.Tag != "" {
		fields = append(fields, [2]string{"o:tag", m.Tag})
	}
	if m.TestMode {
		fields = append(fields, [2]string{"o:testmode", "yes"})
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	var out struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	if err := c.do(ctx, cfg, http.MethodPost, "/v3/"+url.PathEscape(cfg.Domain)+"/messages", w.FormDataContentType(), &body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// DomainInfo is what Mailgun reports about the sending domain.
type DomainInfo struct {
	Name     string
	State    string // active, unverified, disabled
	Type     string // sandbox or custom
	Verified bool   // sending DNS records all valid
}

// Check calls the read-only domain endpoint to confirm the key, region and
// domain agree, and reports whether DNS verification is complete. It sends
// nothing.
func (c *Client) Check(ctx context.Context, cfg Config) (DomainInfo, error) {
	if cfg.APIKey == "" || cfg.Domain == "" {
		return DomainInfo{}, errors.New("email is not configured")
	}
	var out struct {
		Domain struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Type  string `json:"type"`
		} `json:"domain"`
		Sending []struct {
			Valid string `json:"valid"`
		} `json:"sending_dns_records"`
	}
	if err := c.do(ctx, cfg, http.MethodGet, "/v4/domains/"+url.PathEscape(cfg.Domain), "", nil, &out); err != nil {
		return DomainInfo{}, err
	}
	info := DomainInfo{Name: out.Domain.Name, State: out.Domain.State, Type: out.Domain.Type, Verified: len(out.Sending) > 0}
	for _, r := range out.Sending {
		if r.Valid != "valid" {
			info.Verified = false
		}
	}
	return info, nil
}

func (c *Client) do(ctx context.Context, cfg Config, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base(cfg)+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth("api", cfg.APIKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "RivetPanel")
	res, err := c.http().Do(req)
	if err != nil {
		// The error text can carry the URL but never the key (it is a header).
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return &Error{Message: "Mailgun did not answer in time"}
		}
		return &Error{Message: "Mailgun could not be reached"}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if res.StatusCode/100 == 2 {
		if out != nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, out)
		}
		return nil
	}
	return &Error{Status: res.StatusCode, Message: explain(res.StatusCode, raw)}
}

func explain(status int, raw []byte) string {
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &body)
	detail := strings.TrimSpace(body.Message)
	if len(detail) > 200 {
		detail = detail[:200]
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "Mailgun rejected the API key; check the key and that the region matches your account"
	case http.StatusNotFound:
		return "Mailgun does not know that domain in this region; check the domain and the region"
	case http.StatusTooManyRequests:
		return "Mailgun is rate limiting this account; try again shortly"
	case http.StatusPaymentRequired:
		return "Mailgun refused the request: the account's plan or sending limit does not allow it"
	}
	if detail != "" {
		return fmt.Sprintf("Mailgun refused the message (HTTP %d): %s", status, detail)
	}
	return fmt.Sprintf("Mailgun answered with HTTP %d", status)
}
