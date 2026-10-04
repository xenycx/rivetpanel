package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
)

const (
	agentEnrollmentPrefix = "rvt_enroll_"
	defaultEnrollmentTTL  = 15 * time.Minute
	maxEnrollmentTTL      = 24 * time.Hour
)

// AgentEnrollmentStore is the transactional persistence used by enrollment.
type AgentEnrollmentStore interface {
	CreateAgentEnrollment(ctx context.Context, e domain.AgentEnrollment) error
	ListAgentEnrollments(ctx context.Context) ([]domain.AgentEnrollment, error)
	EnrollAgent(ctx context.Context, hash []byte, nowMS int64,
		issue func(nodeID string) (serial string, expiresAtMS int64, err error)) (domain.AgentEnrollment, error)
	RevokeAgentEnrollment(ctx context.Context, id string, nowMS int64) error
	ReissueAgentEnrollment(ctx context.Context, e domain.AgentEnrollment) error
	DeleteUnenrolledAgentNode(ctx context.Context, nodeID string) error
}

// AgentEnrollmentService issues one-use tokens and exchanges them for
// panel-signed, client-only node certificates.
type AgentEnrollmentService struct {
	Store AgentEnrollmentStore
	CA    *agentcert.Authority
	Now   func() time.Time
	// AgentAddress is the host:port agents connect to after enrolling.
	AgentAddress string
}

type AgentCertificate struct {
	NodeID      string
	Certificate string
	CA          string
	Serial      string
	ExpiresAtMS int64
}

func (s *AgentEnrollmentService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Create binds a new agent node to a token whose plaintext is returned once.
func (s *AgentEnrollmentService) Create(ctx context.Context, nodeName, locationID string, ttl time.Duration) (string, domain.AgentEnrollment, error) {
	var err error
	nodeName, err = validateName(nodeName)
	if err != nil {
		return "", domain.AgentEnrollment{}, err
	}
	if locationID == "" {
		locationID = domain.LocalLocationID
	}
	if _, err := uuid.Parse(locationID); err != nil {
		return "", domain.AgentEnrollment{}, domain.Invalid("location id must be a UUID")
	}
	plain, e, err := s.newToken(uuid.NewString(), ttl)
	if err != nil {
		return "", domain.AgentEnrollment{}, err
	}
	e.NodeName, e.LocationID = nodeName, locationID
	if err := s.Store.CreateAgentEnrollment(ctx, e); err != nil {
		return "", domain.AgentEnrollment{}, err
	}
	e.TokenHash = nil
	return plain, e, nil
}

// Reissue atomically replaces the token of a node without a current
// certificate, so a lost, leaked or late token does not require recreating it.
func (s *AgentEnrollmentService) Reissue(ctx context.Context, nodeID string, ttl time.Duration) (string, domain.AgentEnrollment, error) {
	if _, err := uuid.Parse(nodeID); err != nil {
		return "", domain.AgentEnrollment{}, domain.ErrNotFound
	}
	plain, e, err := s.newToken(nodeID, ttl)
	if err != nil {
		return "", domain.AgentEnrollment{}, err
	}
	if err := s.Store.ReissueAgentEnrollment(ctx, e); err != nil {
		return "", domain.AgentEnrollment{}, err
	}
	e.TokenHash = nil
	return plain, e, nil
}

// Discard deletes an agent node that never enrolled, together with its token.
func (s *AgentEnrollmentService) Discard(ctx context.Context, nodeID string) error {
	if _, err := uuid.Parse(nodeID); err != nil {
		return domain.ErrNotFound
	}
	return s.Store.DeleteUnenrolledAgentNode(ctx, nodeID)
}

func (s *AgentEnrollmentService) newToken(nodeID string, ttl time.Duration) (string, domain.AgentEnrollment, error) {
	if ttl == 0 {
		ttl = defaultEnrollmentTTL
	}
	if ttl < time.Minute || ttl > maxEnrollmentTTL {
		return "", domain.AgentEnrollment{}, domain.Invalid("enrollment tokens expire after 1 minute to 24 hours")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", domain.AgentEnrollment{}, err
	}
	plain := agentEnrollmentPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	return plain, domain.AgentEnrollment{ID: uuid.NewString(), NodeID: nodeID,
		Prefix: plain[:len(agentEnrollmentPrefix)+4], TokenHash: auth.HashToken(plain),
		CreatedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(ttl).UnixMilli()}, nil
}

func (s *AgentEnrollmentService) List(ctx context.Context) ([]domain.AgentEnrollment, error) {
	return s.Store.ListAgentEnrollments(ctx)
}

func (s *AgentEnrollmentService) Revoke(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return domain.ErrNotFound
	}
	return s.Store.RevokeAgentEnrollment(ctx, id, s.now().UnixMilli())
}

// Enroll validates the CSR, then consumes the token, signs the certificate and
// records its metadata atomically. Subjects and SANs requested by the agent
// are ignored; the panel supplies the node identity. Any failure after CSR
// validation leaves the token unused.
func (s *AgentEnrollmentService) Enroll(ctx context.Context, token, csrPEM string) (AgentCertificate, error) {
	if s.CA == nil {
		return AgentCertificate{}, domain.ErrNotFound
	}
	if !strings.HasPrefix(token, agentEnrollmentPrefix) || len(token) > 128 {
		return AgentCertificate{}, domain.ErrUnauthorized
	}
	csr, err := agentcert.ParseCSR(csrPEM)
	if err != nil {
		return AgentCertificate{}, domain.Invalid(err.Error())
	}
	var out AgentCertificate
	e, err := s.Store.EnrollAgent(ctx, auth.HashToken(token), s.now().UnixMilli(),
		func(nodeID string) (string, int64, error) {
			cert, serial, expires, err := s.CA.Issue(csr, nodeID)
			if err != nil {
				return "", 0, err
			}
			out = AgentCertificate{NodeID: nodeID, Certificate: string(cert), CA: string(s.CA.CertificatePEM()),
				Serial: serial, ExpiresAtMS: expires}
			return serial, expires, nil
		})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return AgentCertificate{}, domain.ErrUnauthorized
		}
		return AgentCertificate{}, err
	}
	out.NodeID = e.NodeID
	return out, nil
}
