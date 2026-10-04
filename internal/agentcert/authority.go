// Package agentcert owns the private certificate authority used only for
// RivetPanel node-agent client certificates.
package agentcert

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	caKeyFile  = "ca.key"
	caCertFile = "ca.crt"
	maxCSRSize = 16 << 10
)

// Authority signs short-lived client certificates for enrolled nodes.
type Authority struct {
	cert    *x509.Certificate
	key     ed25519.PrivateKey
	certPEM []byte
	now     func() time.Time
}

// LoadOrCreate loads the agent CA from dir, or creates it when neither CA file
// exists. A partial CA is refused instead of silently replacing its identity.
func LoadOrCreate(dir string) (*Authority, error) {
	keyPath, certPath := filepath.Join(dir, caKeyFile), filepath.Join(dir, caCertFile)
	keyInfo, keyErr := os.Stat(keyPath)
	_, certErr := os.Stat(certPath)
	keyExists, certExists := keyErr == nil, certErr == nil
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return nil, keyErr
	}
	if certErr != nil && !errors.Is(certErr, os.ErrNotExist) {
		return nil, certErr
	}
	if keyExists != certExists {
		return nil, errors.New("agent CA is incomplete: ca.key and ca.crt must both exist")
	}
	if !keyExists {
		if err := create(dir, keyPath, certPath); err != nil {
			return nil, err
		}
		keyInfo, _ = os.Stat(keyPath)
	}
	if keyInfo.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("agent CA key %s must not be group/world accessible (chmod 600)", keyPath)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	kb, rest := pem.Decode(keyPEM)
	if kb == nil || len(rest) != 0 || kb.Type != "PRIVATE KEY" {
		return nil, errors.New("agent CA key is not one PKCS#8 private key")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse agent CA key: %w", err)
	}
	key, ok := parsedKey.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("agent CA key is not Ed25519")
	}
	cb, rest := pem.Decode(certPEM)
	if cb == nil || len(rest) != 0 || cb.Type != "CERTIFICATE" {
		return nil, errors.New("agent CA certificate is not one PEM certificate")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse agent CA certificate: %w", err)
	}
	certPublic, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok || !cert.IsCA || !cert.BasicConstraintsValid || !certPublic.Equal(key.Public()) {
		return nil, errors.New("agent CA certificate does not match its key")
	}
	if now := time.Now().UTC(); now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return nil, errors.New("agent CA certificate is not currently valid")
	}
	return &Authority{cert: cert, key: key, certPEM: certPEM}, nil
}

func create(dir, keyPath, certPath string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "RivetPanel agent CA"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kf, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = kf.Write(keyOut); err == nil {
		err = kf.Sync()
	}
	closeErr := kf.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	cf, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err = cf.Write(certOut); err == nil {
		err = cf.Sync()
	}
	if closeErr := cf.Close(); err == nil {
		err = closeErr
	}
	return err
}

// ParseCSR validates the request before a single-use token is consumed. The
// requested subject and SANs are intentionally ignored when issuing a cert.
func ParseCSR(csrPEM string) (*x509.CertificateRequest, error) {
	if len(csrPEM) == 0 || len(csrPEM) > maxCSRSize {
		return nil, errors.New("certificate request must be 1 to 16384 bytes")
	}
	b, rest := pem.Decode([]byte(csrPEM))
	if b == nil || len(rest) != 0 || b.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("certificate request must contain one PEM CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(b.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return nil, errors.New("certificate request signature is invalid")
	}
	switch key := csr.PublicKey.(type) {
	case ed25519.PublicKey:
	case *ecdsa.PublicKey:
		bits := key.Curve.Params().BitSize
		if bits != 256 && bits != 384 {
			return nil, errors.New("certificate request key must use Ed25519, P-256 or P-384")
		}
	default:
		return nil, errors.New("certificate request key must use Ed25519, P-256 or P-384")
	}
	return csr, nil
}

// Issue signs a client-only certificate for exactly one node identity.
func (a *Authority) Issue(csr *x509.CertificateRequest, nodeID string) (certPEM []byte, serial string, expiresAtMS int64, err error) {
	now := time.Now().UTC()
	if a.now != nil {
		now = a.now().UTC()
	}
	serialNumber, err := randomSerial()
	if err != nil {
		return nil, "", 0, err
	}
	identity, _ := url.Parse("spiffe://rivetpanel/node/" + nodeID)
	notAfter := now.Add(30 * 24 * time.Hour)
	if notAfter.After(a.cert.NotAfter) {
		notAfter = a.cert.NotAfter
	}
	if !notAfter.After(now) {
		return nil, "", 0, errors.New("agent CA expires too soon to issue a certificate")
	}
	tpl := &x509.Certificate{SerialNumber: serialNumber,
		Subject:   pkix.Name{CommonName: "rivet-agent:" + nodeID},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs: []*url.URL{identity}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, a.cert, csr.PublicKey, a.key)
	if err != nil {
		return nil, "", 0, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		hex.EncodeToString(serialNumber.Bytes()), tpl.NotAfter.UnixMilli(), nil
}

func (a *Authority) CertificatePEM() []byte { return append([]byte(nil), a.certPEM...) }

func randomSerial() (*big.Int, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	b[0] &= 0x7f
	n := new(big.Int).SetBytes(b)
	if n.Sign() == 0 {
		n.SetInt64(1)
	}
	return n, nil
}

// Pool returns a certificate pool holding only this CA.
func (a *Authority) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(a.cert)
	return p
}

// IssueServer creates the agent listener's TLS certificate for the given host
// names and IP addresses, signed by this CA. Agents trust only this CA, so a
// certificate from a public CA cannot impersonate the panel to them.
func (a *Authority) IssueServer(hosts []string, lifetime time.Duration) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := randomSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now().UTC()
	notAfter := now.Add(lifetime)
	if notAfter.After(a.cert.NotAfter) {
		notAfter = a.cert.NotAfter
	}
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "rivetpanel agent listener"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else if h != "" {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, a.cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}

// NodeIdentity extracts the node UUID and serial from a verified client
// certificate issued by Issue. It checks the client-auth usage and the single
// spiffe://rivetpanel/node/<uuid> identity.
func NodeIdentity(c *x509.Certificate) (nodeID, serial string, err error) {
	clientAuth := false
	for _, u := range c.ExtKeyUsage {
		clientAuth = clientAuth || u == x509.ExtKeyUsageClientAuth
	}
	if !clientAuth {
		return "", "", errors.New("certificate is not for client authentication")
	}
	if len(c.URIs) != 1 || c.URIs[0].Scheme != "spiffe" || c.URIs[0].Host != "rivetpanel" ||
		!strings.HasPrefix(c.URIs[0].Path, "/node/") {
		return "", "", errors.New("certificate has no RivetPanel node identity")
	}
	nodeID = strings.TrimPrefix(c.URIs[0].Path, "/node/")
	if len(nodeID) != 36 || strings.ContainsAny(nodeID, "/?#") {
		return "", "", errors.New("certificate has an invalid node identity")
	}
	return nodeID, hex.EncodeToString(c.SerialNumber.Bytes()), nil
}
