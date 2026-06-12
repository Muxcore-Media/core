package grpcmesh

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	caCertDuration       = 10 * 365 * 24 * time.Hour
	moduleCertDuration   = 365 * 24 * time.Hour
	tokenTTL             = 5 * time.Minute
	tokenVersion         = "1"
	tokenPrefix          = "mct_" + tokenVersion + "_"
	tokenCleanupInterval = 1 * time.Minute
)

var serialLimit = new(big.Int).Lsh(big.NewInt(1), 128) // 2^128

// CertAuthority manages an internal CA for issuing module certificates.
// Thread-safe. Not a full PKI — no CRL/OCSP support.
type CertAuthority struct {
	mu        sync.RWMutex
	caCert    *x509.Certificate
	caKey     crypto.Signer
	caCertPEM []byte
	dataDir   string

	// Token store: hash(token) → token record
	tokens    map[string]*tokenRecord
	cleanupCh chan struct{}
	cleanupWG sync.WaitGroup
}

type tokenRecord struct {
	moduleID  string
	expiresAt time.Time
	used      bool
}

// NewCertAuthority creates or loads a CA in the given directory.
// If the CA doesn't exist, generates one.
// dir: path like "<data-dir>/muxcore/ca"
func NewCertAuthority(dir string) (*CertAuthority, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("certauth: create dir %s: %w", dir, err)
	}

	ca := &CertAuthority{
		dataDir:   dir,
		tokens:    make(map[string]*tokenRecord),
		cleanupCh: make(chan struct{}),
	}

	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	if _, err := os.Stat(certPath); err == nil {
		if err := ca.loadCA(certPath, keyPath); err != nil {
			return nil, fmt.Errorf("certauth: load CA: %w", err)
		}
		slog.Info("certauth: loaded existing CA", "path", certPath)
	} else {
		if err := ca.generateCA(certPath, keyPath); err != nil {
			return nil, fmt.Errorf("certauth: generate CA: %w", err)
		}
		slog.Info("certauth: generated new CA", "path", certPath)
	}

	ca.startTokenCleanup()
	return ca, nil
}

// Close stops the background token cleanup goroutine.
func (ca *CertAuthority) Close() {
	close(ca.cleanupCh)
	ca.cleanupWG.Wait()
}

func (ca *CertAuthority) loadCA(certPath, keyPath string) error {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return fmt.Errorf("no PEM block in CA cert")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("no PEM block in CA key")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return fmt.Errorf("CA key is not a signer")
	}

	ca.caCert = cert
	ca.caKey = signer
	ca.caCertPEM = certPEM
	return nil
}

func (ca *CertAuthority) generateCA(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "MuxCore Internal CA",
			Organization: []string{"MuxCore"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(caCertDuration),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return err
	}

	ca.caCert = cert
	ca.caKey = key
	ca.caCertPEM = certPEM
	return nil
}

// CACertPEM returns the CA certificate in PEM format (for TLS config).
func (ca *CertAuthority) CACertPEM() []byte {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.caCertPEM
}

// IssueModuleCert generates a keypair and signs a certificate for the
// given module ID. Returns (certPEM, keyPEM, error).
func (ca *CertAuthority) IssueModuleCert(moduleID string, ips []net.IP, dnsNames []string) ([]byte, []byte, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate module key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   moduleID,
			Organization: []string{"MuxCore"},
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(moduleCertDuration),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		IPAddresses: ips,
		DNSNames:    dnsNames,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.caCert, &key.PublicKey, ca.caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create module cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal module key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

// IssueModuleCertForDir writes cert and key files to the given directory.
// Returns (certPath, keyPath, error).
func (ca *CertAuthority) IssueModuleCertForDir(moduleID string, dir string) (string, string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("create cert dir for %s: %w", moduleID, err)
	}

	certPEM, keyPEM, err := ca.IssueModuleCert(moduleID,
		[]net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		[]string{"localhost"},
	)
	if err != nil {
		return "", "", err
	}

	certPath := filepath.Join(dir, "module.crt")
	keyPath := filepath.Join(dir, "module.key")

	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return "", "", err
	}

	return certPath, keyPath, nil
}

// GenerateToken creates a one-time use bootstrap token for a module ID.
// Returns the raw token string (must be delivered out-of-band to the module).
func (ca *CertAuthority) GenerateToken(moduleID string) (string, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate token random: %w", err)
	}

	token := tokenPrefix + moduleID + "_" + hex.EncodeToString(random)

	hash := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(hash[:])

	ca.tokens[key] = &tokenRecord{
		moduleID:  moduleID,
		expiresAt: time.Now().Add(tokenTTL),
	}

	return token, nil
}

// ValidateToken checks a bootstrap token and returns the claimed module ID
// if valid. Single-use: after validation, the token is consumed.
func (ca *CertAuthority) ValidateToken(token string) (string, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	if !strings.HasPrefix(token, tokenPrefix) {
		return "", fmt.Errorf("invalid token format")
	}
	rest := strings.TrimPrefix(token, tokenPrefix)
	parts := strings.SplitN(rest, "_", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid token format: missing module ID")
	}
	claimedModule := parts[0]

	hash := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(hash[:])

	rec, exists := ca.tokens[key]
	if !exists {
		return "", fmt.Errorf("token not found")
	}
	if rec.used {
		return "", fmt.Errorf("token already used")
	}
	if time.Now().After(rec.expiresAt) {
		return "", fmt.Errorf("token expired")
	}
	if rec.moduleID != claimedModule {
		return "", fmt.Errorf("token module ID mismatch: %q vs %q", rec.moduleID, claimedModule)
	}

	rec.used = true
	return claimedModule, nil
}

// startTokenCleanup launches a background goroutine that periodically evicts
// expired token records to prevent unbounded memory growth.
func (ca *CertAuthority) startTokenCleanup() {
	ca.cleanupWG.Add(1)
	go func() {
		defer ca.cleanupWG.Done()
		ticker := time.NewTicker(tokenCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ca.cleanupCh:
				return
			case <-ticker.C:
				ca.mu.Lock()
				now := time.Now()
				for k, rec := range ca.tokens {
					if now.After(rec.expiresAt) {
						delete(ca.tokens, k)
					}
				}
				ca.mu.Unlock()
			}
		}
	}()
}

// MTLSConfig builds a tls.Config configured with the CA cert for client
// verification (mTLS). When ca is nil, returns nil (no mTLS).
func MTLSConfig(ca *CertAuthority) *tls.Config {
	if ca == nil {
		return nil
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CACertPEM())
	return &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  pool,
		MinVersion: tls.VersionTLS12,
	}
}
