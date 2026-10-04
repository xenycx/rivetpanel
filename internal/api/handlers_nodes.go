package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/lazyre"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// AgentControl can terminate a live agent session after an administrator
// disables a node or revokes its identity.
type AgentControl interface {
	Disconnect(nodeID string)
}

// NodeStore is the persistence surface for node and telemetry reads.
type NodeStore interface {
	ListNodes(ctx context.Context) ([]domain.Node, error)
	GetNode(ctx context.Context, id string) (domain.Node, error)
	UpdateNode(ctx context.Context, id string, u sqlite.NodeUpdate, nowMS int64) error
	DeleteAgentNode(ctx context.Context, id string) error
	CountNodeBots(ctx context.Context, nodeID string) (int, error)
	ListAgentStates(ctx context.Context) (map[string]domain.AgentState, error)
	RevokeAgentCertificates(ctx context.Context, nodeID, reason string, nowMS int64) (int, error)
	ListLocations(ctx context.Context) ([]domain.Location, map[string]int, error)
	CreateLocation(ctx context.Context, name, description string, nowMS int64) (domain.Location, error)
	UpdateLocation(ctx context.Context, id, name, description string, nowMS int64) error
	DeleteLocation(ctx context.Context, id string) error
	ListTelemetry(ctx context.Context, nodeID string, sinceMS int64, limit int) ([]domain.Telemetry, error)
	ListTelemetryBuckets(ctx context.Context, nodeID string, sinceMS, bucketMS int64) ([]domain.TelemetryBucket, error)
}

const (
	defaultTelemetryLimit = 120
	maxTelemetryLimit     = 2880 // a day at 30 s
)

type nodeDTO struct {
	ID            string    `json:"id"`
	LocationID    string    `json:"location_id"`
	Name          string    `json:"name"`
	Transport     string    `json:"transport"`
	Enabled       bool      `json:"enabled"`
	LastSeenMS    *int64    `json:"last_seen_at_ms"`
	Draining      bool      `json:"draining"`
	PublicAddress string    `json:"public_address"`
	ServerCount   int       `json:"server_count"`
	Agent         *agentDTO `json:"agent,omitempty"`
}

type agentDTO struct {
	Connected              bool            `json:"connected"`
	ProtocolVersion        int             `json:"protocol_version"`
	AgentVersion           string          `json:"agent_version"`
	Hostname               string          `json:"hostname"`
	Capabilities           json.RawMessage `json:"capabilities"`
	CertificateSerial      *string         `json:"certificate_serial"`
	CertificateExpiresAtMS *int64          `json:"certificate_expires_at_ms"`
	ConnectedAtMS          *int64          `json:"connected_at_ms"`
	DisconnectedAtMS       *int64          `json:"disconnected_at_ms"`
}

type sampleDTO struct {
	SampledAtMS      int64   `json:"sampled_at_ms"`
	CPUPercent       float64 `json:"cpu_percent"`
	LogicalCPUs      int     `json:"logical_cpus"`
	MemoryUsedBytes  int64   `json:"memory_used_bytes"`
	MemoryTotalBytes int64   `json:"memory_total_bytes"`
	DiskUsedBytes    int64   `json:"disk_used_bytes"`
	DiskTotalBytes   int64   `json:"disk_total_bytes"`
	RunningBots      int     `json:"running_bots"`
	Load1            float64 `json:"load1"`
	SwapUsedBytes    int64   `json:"swap_used_bytes"`
	SwapTotalBytes   int64   `json:"swap_total_bytes"`
	NetRxBps         int64   `json:"net_rx_bps"`
	NetTxBps         int64   `json:"net_tx_bps"`
	DiskReadBps      int64   `json:"disk_read_bps"`
	DiskWriteBps     int64   `json:"disk_write_bps"`
}

// listNodes returns nodes with their latest sample. Host-level data is
// administrator-only; regular users see only their own bots' states.
func (s *panel) listNodes(c fiber.Ctx) error {
	nodes, err := s.nodes.ListNodes(c.Context())
	if err != nil {
		return err
	}
	states, err := s.nodes.ListAgentStates(c.Context())
	if err != nil {
		return err
	}
	type item struct {
		nodeDTO
		Latest *sampleDTO `json:"latest,omitempty"`
	}
	out := make([]item, 0, len(nodes))
	for _, n := range nodes {
		count, err := s.nodes.CountNodeBots(c.Context(), n.ID)
		if err != nil {
			return err
		}
		base := nodeDTO{ID: n.ID, LocationID: n.LocationID, Name: n.Name, Transport: n.Transport, Enabled: n.Enabled,
			LastSeenMS: n.LastSeenMS, Draining: n.Draining, PublicAddress: n.PublicAddress, ServerCount: count}
		if st, ok := states[n.ID]; ok {
			caps := json.RawMessage(st.CapabilitiesJSON)
			base.Agent = &agentDTO{Connected: st.Connected, ProtocolVersion: st.ProtocolVersion, AgentVersion: st.AgentVersion,
				Hostname: st.Hostname, Capabilities: caps, CertificateSerial: st.CertificateSerial,
				CertificateExpiresAtMS: st.CertificateExpiresAtMS, ConnectedAtMS: st.ConnectedAtMS, DisconnectedAtMS: st.DisconnectedAtMS}
		}
		it := item{nodeDTO: base}
		if rows, err := s.nodes.ListTelemetry(c.Context(), n.ID, 0, 1); err == nil && len(rows) == 1 {
			d := toSample(rows[0])
			it.Latest = &d
		}
		out = append(out, it)
	}
	return c.JSON(fiber.Map{"nodes": out})
}

var publicAddressRE = lazyre.New(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func validLabel(v, what string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if n := utf8.RuneCountInString(v); n < 1 || n > max || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", domain.Invalid(fmt.Sprintf("%s must be 1-%d characters without control characters", what, max))
	}
	return v, nil
}

func (s *panel) patchNode(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	var in struct {
		Name          *string `json:"name"`
		LocationID    *string `json:"location_id"`
		Enabled       *bool   `json:"enabled"`
		Draining      *bool   `json:"draining"`
		PublicAddress *string `json:"public_address"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Name != nil {
		v, err := validLabel(*in.Name, "name", 64)
		if err != nil {
			return err
		}
		in.Name = &v
	}
	if in.PublicAddress != nil {
		v := strings.TrimSpace(*in.PublicAddress)
		if v != "" && (len(v) > 253 || (net.ParseIP(v) == nil && !publicAddressRE.MatchString(v))) {
			return domain.Invalid("public address must be a host name or IP address without a scheme or port")
		}
		in.PublicAddress = &v
	}
	if err := s.nodes.UpdateNode(c.Context(), id, sqlite.NodeUpdate{Name: in.Name, LocationID: in.LocationID,
		Enabled: in.Enabled, Draining: in.Draining, PublicAddress: in.PublicAddress}, time.Now().UnixMilli()); err != nil {
		return err
	}
	if in.Enabled != nil && !*in.Enabled && s.agentControl != nil {
		s.agentControl.Disconnect(id)
	}
	n, err := s.nodes.GetNode(c.Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(nodeDTO{ID: n.ID, LocationID: n.LocationID, Name: n.Name, Transport: n.Transport, Enabled: n.Enabled,
		LastSeenMS: n.LastSeenMS, Draining: n.Draining, PublicAddress: n.PublicAddress})
}

func (s *panel) deleteNode(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	if err := s.nodes.DeleteAgentNode(c.Context(), id); err != nil {
		return err
	}
	if s.agentControl != nil {
		s.agentControl.Disconnect(id)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) revokeNodeCertificates(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	n, err := s.nodes.GetNode(c.Context(), id)
	if err != nil {
		return err
	}
	if !n.IsAgent() {
		return domain.Invalid("only agent nodes have certificates")
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if len(c.Body()) != 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		in.Reason = "revoked by administrator"
	}
	if len(in.Reason) > 200 {
		return domain.Invalid("reason must be at most 200 characters")
	}
	count, err := s.nodes.RevokeAgentCertificates(c.Context(), id, in.Reason, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if s.agentControl != nil {
		s.agentControl.Disconnect(id)
	}
	return c.JSON(fiber.Map{"revoked": count})
}

type locationDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	NodeCount   int    `json:"node_count"`
	CreatedAtMS int64  `json:"created_at_ms"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

func (s *panel) listLocations(c fiber.Ctx) error {
	rows, counts, err := s.nodes.ListLocations(c.Context())
	if err != nil {
		return err
	}
	out := make([]locationDTO, 0, len(rows))
	for _, l := range rows {
		out = append(out, locationDTO{l.ID, l.Name, l.Description, counts[l.ID], l.CreatedAtMS, l.UpdatedAtMS})
	}
	return c.JSON(fiber.Map{"locations": out})
}

func locationInput(c fiber.Ctx) (string, string, error) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(c, &in); err != nil {
		return "", "", err
	}
	name, err := validLabel(in.Name, "location name", 64)
	if err != nil {
		return "", "", err
	}
	desc := strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(desc) > 500 {
		return "", "", domain.Invalid("description must be at most 500 characters")
	}
	return name, desc, nil
}

func (s *panel) createLocation(c fiber.Ctx) error {
	name, desc, err := locationInput(c)
	if err != nil {
		return err
	}
	l, err := s.nodes.CreateLocation(c.Context(), name, desc, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(locationDTO{l.ID, l.Name, l.Description, 0, l.CreatedAtMS, l.UpdatedAtMS})
}

func (s *panel) patchLocation(c fiber.Ctx) error {
	name, desc, err := locationInput(c)
	if err != nil {
		return err
	}
	id := strings.Clone(c.Params("id"))
	now := time.Now().UnixMilli()
	if err := s.nodes.UpdateLocation(c.Context(), id, name, desc, now); err != nil {
		return err
	}
	return c.JSON(locationDTO{ID: id, Name: name, Description: desc, UpdatedAtMS: now})
}

func (s *panel) deleteLocation(c fiber.Ctx) error {
	if err := s.nodes.DeleteLocation(c.Context(), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) createAgentEnrollment(c fiber.Ctx) error {
	var in struct {
		Name       string `json:"name"`
		LocationID string `json:"location_id"`
		TTLMinutes int    `json:"ttl_minutes"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	plain, e, err := s.enrollment.Create(c.Context(), in.Name, in.LocationID, time.Duration(in.TTLMinutes)*time.Minute)
	if err != nil {
		return err
	}
	return s.agentEnrollmentResponse(c, plain, e)
}

func (s *panel) reissueAgentEnrollment(c fiber.Ctx) error {
	var in struct {
		TTLMinutes int `json:"ttl_minutes"`
	}
	if len(c.Body()) != 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	plain, e, err := s.enrollment.Reissue(c.Context(), strings.Clone(c.Params("id")), time.Duration(in.TTLMinutes)*time.Minute)
	if err != nil {
		return err
	}
	return s.agentEnrollmentResponse(c, plain, e)
}

func (s *panel) agentEnrollmentResponse(c fiber.Ctx, plain string, e domain.AgentEnrollment) error {
	panelURL := s.currentPublicURL()
	if panelURL == "" {
		panelURL = c.Protocol() + "://" + c.Get("Host")
	}
	command := "rivet-agent enroll --panel " + panelURL + " --token " + plain
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"node_id": e.NodeID, "token_id": e.ID, "expires_at_ms": e.ExpiresAtMS,
		"token": plain, "command": command})
}

func toSample(t domain.Telemetry) sampleDTO {
	return sampleDTO{t.SampledAtMS, t.CPUPercent, t.LogicalCPUs, t.MemoryUsedBytes, t.MemoryTotalBytes,
		t.DiskUsedBytes, t.DiskTotalBytes, t.RunningBots, t.Load1, t.SwapUsedBytes, t.SwapTotalBytes,
		t.NetRxBps, t.NetTxBps, t.DiskReadBps, t.DiskWriteBps}
}

// nodeTelemetry returns up to `limit` of the newest samples after `since_ms`,
// oldest first.
func (s *panel) nodeTelemetry(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	if _, err := s.nodes.GetNode(c.Context(), id); err != nil {
		return err
	}
	limit, since := defaultTelemetryLimit, int64(0)
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxTelemetryLimit {
			return domain.Invalid("limit must be between 1 and " + strconv.Itoa(maxTelemetryLimit))
		}
		limit = n
	}
	if v := c.Query("since_ms"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return domain.Invalid("since_ms must be a non-negative integer")
		}
		since = n
	}
	rows, err := s.nodes.ListTelemetry(c.Context(), id, since, limit)
	if err != nil {
		return err
	}
	out := make([]sampleDTO, len(rows))
	for i, r := range rows {
		out[i] = toSample(r)
	}
	return c.JSON(fiber.Map{"node_id": id, "samples": out})
}
