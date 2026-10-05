package grpcmesh

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Muxcore-Media/core/internal/enroll"
)

// ConfigureEnrollment enables ADR-0017 enrollment on the CA: version-2
// tokens (mct_2_…) verified with secret and made single-use by the ledger
// in the CA directory, and the SAN allow-list for module certificates.
// An empty secret leaves only the in-memory version-1 tokens; the SAN
// allow-list applies either way.
func (ca *CertAuthority) ConfigureEnrollment(secret, sanAllow string) error {
	policy := enroll.ParseSANAllow(sanAllow)
	var v *enroll.Verifier
	if secret != "" {
		var err error
		v, err = enroll.NewVerifier(secret, enroll.NewLedger(ca.dataDir))
		if err != nil {
			return err
		}
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.enroll = v
	ca.sanPolicy = policy
	return nil
}

// EnrollmentEnabled reports whether version-2 tokens are accepted.
func (ca *CertAuthority) EnrollmentEnabled() bool {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.enroll != nil
}

// Dir returns the CA directory (holds ca.crt, ca.key and enrolled.json).
func (ca *CertAuthority) Dir() string { return ca.dataDir }

// EnrollmentSANs returns the SANs for a module certificate: loopback,
// localhost, the module ID and the requested names allowed by
// MUXCORE_ENROLL_SAN_ALLOW. dropped lists the requested names refused.
func (ca *CertAuthority) EnrollmentSANs(moduleID string, requested []string) (ips []net.IP, dns, dropped []string) {
	ca.mu.RLock()
	p := ca.sanPolicy
	ca.mu.RUnlock()
	return p.SANs(moduleID, requested)
}

// validateEnrollToken validates a version-2 token (caller holds no lock).
func (ca *CertAuthority) validateEnrollToken(token string) (string, error) {
	ca.mu.RLock()
	v := ca.enroll
	ca.mu.RUnlock()
	if v == nil {
		return "", enroll.ErrNoSecret
	}
	return v.Validate(token)
}

// SignModuleCSR issues a certificate for moduleID over the public key of
// csr (the module keeps its private key; core writes nothing). The subject
// and SANs in the request are ignored: CN is moduleID and the SANs are the
// given ips and dnsNames. Returns the PEM certificate.
func (ca *CertAuthority) SignModuleCSR(moduleID string, csr *x509.CertificateRequest, ips []net.IP, dnsNames []string) ([]byte, error) {
	if csr == nil {
		return nil, errors.New("nil CSR")
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature: %w", err)
	}
	if err := enroll.ValidModuleID(moduleID); err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   moduleID,
			Organization: []string{"MuxCore"},
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(moduleCertDuration),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		IPAddresses: ips,
		DNSNames:    dnsNames,
	}
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	der, err := x509.CreateCertificate(rand.Reader, template, ca.caCert, csr.PublicKey, ca.caKey)
	if err != nil {
		return nil, fmt.Errorf("sign module CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}
