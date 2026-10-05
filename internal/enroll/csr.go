package enroll

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
)

// maxCSRBytes bounds the CSR accepted from an unauthenticated caller.
const maxCSRBytes = 16 << 10

// ParseCSR decodes a PEM PKCS#10 request, checks its self-signature and
// requires a key type core is willing to certify (ECDSA P-256/P-384,
// Ed25519, or RSA >= 2048 bits). Only the public key of the request is used;
// its subject and SANs are ignored.
func ParseCSR(csrPEM string) (*x509.CertificateRequest, error) {
	if len(csrPEM) > maxCSRBytes {
		return nil, errors.New("CSR too large")
	}
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("csr_pem is not a PEM CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature: %w", err)
	}
	switch pub := csr.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if pub.Curve != elliptic.P256() && pub.Curve != elliptic.P384() {
			return nil, errors.New("CSR key: ECDSA curve must be P-256 or P-384")
		}
	case ed25519.PublicKey:
	case *rsa.PublicKey:
		if pub.N.BitLen() < 2048 {
			return nil, errors.New("CSR key: RSA keys must be at least 2048 bits")
		}
	default:
		return nil, fmt.Errorf("CSR key: unsupported key type %T", csr.PublicKey)
	}
	return csr, nil
}

// SANPolicy decides which requested names a module may carry as SANs.
// The zero value allows none (module ID and loopback only).
type SANPolicy struct {
	allow []string
}

// ParseSANAllow parses MUXCORE_ENROLL_SAN_ALLOW: comma-separated exact names
// (or IP addresses) and "*.suffix" wildcards matching any name below suffix.
func ParseSANAllow(s string) SANPolicy {
	var p SANPolicy
	for _, e := range strings.Split(s, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		p.allow = append(p.allow, e)
	}
	return p
}

// Allow returns the allow-list entries.
func (p SANPolicy) Allow() []string { return append([]string(nil), p.allow...) }

// Allowed reports whether name may be added as a SAN.
func (p SANPolicy) Allowed(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == "" {
		return false
	}
	isIP := net.ParseIP(name) != nil
	for _, a := range p.allow {
		if suffix, ok := strings.CutPrefix(a, "*."); ok {
			if isIP {
				continue // wildcards match DNS names only
			}
			if strings.HasSuffix(name, "."+suffix) && len(name) > len(suffix)+1 {
				return true
			}
			continue
		}
		if name == a {
			return true
		}
	}
	return false
}

// SANs returns the SANs for moduleID's certificate: 127.0.0.1, ::1,
// localhost and the module ID, plus every requested name the policy allows
// (IP literals become IP SANs). The second result lists the requested names
// that were dropped.
func (p SANPolicy) SANs(moduleID string, requested []string) (ips []net.IP, dns, dropped []string) {
	ips = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	dns = []string{"localhost"}
	seen := map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true}
	if moduleID != "" && !seen[strings.ToLower(moduleID)] {
		dns = append(dns, moduleID)
		seen[strings.ToLower(moduleID)] = true
	}
	for _, r := range requested {
		n := strings.TrimSuffix(strings.TrimSpace(r), ".")
		key := strings.ToLower(n)
		if n == "" || seen[key] {
			continue
		}
		if !p.Allowed(n) {
			dropped = append(dropped, r)
			continue
		}
		seen[key] = true
		if ip := net.ParseIP(n); ip != nil {
			ips = append(ips, ip)
			continue
		}
		dns = append(dns, key)
	}
	return ips, dns, dropped
}
