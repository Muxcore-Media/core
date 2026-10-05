// Package meshid gives a module its mesh identity (ADR-0017 decision 4).
//
// Ensure is called once at module start-up, before the module dials core:
//
//  1. If MUXCORE_TLS_CERT and MUXCORE_TLS_KEY (or Config.CertFile/KeyFile)
//     are already set, they are used unchanged.
//  2. Else, if module.crt and module.key exist in the identity directory
//     (MUXCORE_TLS_DIR, default <MUXCORE_DATA_DIR or ./data>/mesh-id), they
//     are reused without contacting core.
//  3. Else, if MUXCORE_BOOTSTRAP_TOKEN and the core CA (MUXCORE_TLS_CA, or
//     ca.crt in MUXCORE_CA_EXPORT_DIR) are available, the module enrolls:
//     it generates an ECDSA P-256 key locally, dials core with
//     server-verified TLS (no client certificate) and sends a CSR through
//     ModuleRegistration.BootstrapRegister. The signed certificate, the key
//     and the CA are stored in the identity directory (files 0600, dir 0700).
//
// In every case Ensure exports MUXCORE_TLS_CERT, MUXCORE_TLS_KEY and
// MUXCORE_TLS_CA for the process, so modulesdk.Run, client.Dial and the
// modules' own TLS helpers pick the identity up.
//
// With the insecure flag (dev profile) Ensure does nothing. In the
// household profile the insecure flag is an error (ADR-0016).
package meshid

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

// Environment variables read (and, for the TLS paths, written) by Ensure.
const (
	EnvTLSDir         = "MUXCORE_TLS_DIR"
	EnvTLSCert        = "MUXCORE_TLS_CERT"
	EnvTLSKey         = "MUXCORE_TLS_KEY"
	EnvTLSCA          = "MUXCORE_TLS_CA"
	EnvBootstrapToken = "MUXCORE_BOOTSTRAP_TOKEN" //nolint:gosec // env var name, not a credential
	EnvCAExportDir    = "MUXCORE_CA_EXPORT_DIR"
	EnvDataDir        = "MUXCORE_DATA_DIR"
	EnvGRPCAddr       = "MUXCORE_GRPC_ADDR"
	EnvModuleID       = "MUXCORE_MODULE_ID"
	// EnvEnrollDNSNames is a comma-separated list of extra DNS SANs to
	// request (core keeps only those in its MUXCORE_ENROLL_SAN_ALLOW).
	EnvEnrollDNSNames = "MUXCORE_ENROLL_DNS_NAMES"
	// EnvTLSServerName overrides the name used to verify core's certificate
	// (default: the host of the core address).
	EnvTLSServerName = "MUXCORE_TLS_SERVER_NAME"
)

// File names inside the identity directory.
const (
	CertFile = "module.crt"
	KeyFile  = "module.key"
	CAFile   = "ca.crt"
)

// DefaultDirName is the identity directory under the module data dir.
const DefaultDirName = "mesh-id"

const defaultTimeout = 60 * time.Second

// Config configures Ensure. Empty fields fall back to the environment.
type Config struct {
	// Getenv and Setenv default to os.Getenv and os.Setenv (tests).
	Getenv func(string) string
	Setenv func(key, value string) error

	// ModuleID is the module's ID (MUXCORE_MODULE_ID). Required to enroll.
	ModuleID string
	// GRPCAddr is core's gRPC address (MUXCORE_GRPC_ADDR). Required to enroll.
	GRPCAddr string
	// CertFile/KeyFile are explicitly configured client certificate paths;
	// when both are set Ensure uses them unchanged.
	CertFile string
	KeyFile  string
	// CAFile is the core CA used to verify core (MUXCORE_TLS_CA, else
	// MUXCORE_CA_EXPORT_DIR/ca.crt, else the stored ca.crt).
	CAFile string
	// Dir is the identity directory (MUXCORE_TLS_DIR, else
	// <DataDir>/mesh-id).
	Dir string
	// DataDir is the module data directory (MUXCORE_DATA_DIR, else ./data).
	DataDir string
	// Token is the single-use enrollment token (MUXCORE_BOOTSTRAP_TOKEN).
	Token string
	// DNSNames are extra SANs to request (MUXCORE_ENROLL_DNS_NAMES).
	DNSNames []string
	// Timeout bounds enrollment, including waiting for core to come up.
	// Default 60s.
	Timeout time.Duration
	// Insecure is the module's plaintext/dev setting. The insecure env flag
	// (MUXCORE_INSECURE_DISABLE_TLS) is honoured as well.
	Insecure bool
}

// Paths are the identity files in use. All empty when Ensure was a no-op.
type Paths struct {
	Cert string
	Key  string
	CA   string
	// Enrolled is true when this call enrolled with core.
	Enrolled bool
}

// ErrNoIdentity is returned when there is no certificate, no stored
// identity and no enrollment token.
var ErrNoIdentity = errors.New("no mesh identity")

// Ensure makes sure the module has a mesh identity; see the package doc.
func Ensure(ctx context.Context, cfg Config) (Paths, error) {
	getenv, setenv := cfg.Getenv, cfg.Setenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if setenv == nil {
		setenv = os.Setenv
	}
	insecure := cfg.Insecure || InsecureFromEnv(getenv)
	if err := CheckProfile(getenv, insecure); err != nil {
		return Paths{}, err
	}
	if insecure {
		slog.Warn("meshid: insecure dev mode — no mesh identity; core sees this module only by its claimed ID")
		return Paths{}, nil
	}

	pick := func(v, env string) string {
		if v != "" {
			return v
		}
		return strings.TrimSpace(getenv(env))
	}
	certFile := pick(cfg.CertFile, EnvTLSCert)
	keyFile := pick(cfg.KeyFile, EnvTLSKey)
	caFile := pick(cfg.CAFile, EnvTLSCA)
	if caFile == "" {
		if d := strings.TrimSpace(getenv(EnvCAExportDir)); d != "" {
			if _, err := os.Stat(filepath.Join(d, CAFile)); err == nil {
				caFile = filepath.Join(d, CAFile)
			}
		}
	}

	// 1. Explicit certificate.
	if certFile != "" && keyFile != "" {
		return export(setenv, Paths{Cert: certFile, Key: keyFile, CA: caFile})
	}

	dir := pick(cfg.Dir, EnvTLSDir)
	if dir == "" {
		data := pick(cfg.DataDir, EnvDataDir)
		if data == "" {
			data = "data"
		}
		dir = filepath.Join(data, DefaultDirName)
	}
	moduleID := pick(cfg.ModuleID, EnvModuleID)
	p := Paths{
		Cert: filepath.Join(dir, CertFile),
		Key:  filepath.Join(dir, KeyFile),
		CA:   caFile,
	}
	if p.CA == "" {
		p.CA = filepath.Join(dir, CAFile)
	}

	// 2. Stored identity.
	certExists, keyExists := exists(p.Cert), exists(p.Key)
	switch {
	case certExists && keyExists:
		if err := checkStored(p, moduleID); err != nil {
			return Paths{}, err
		}
		if !exists(p.CA) {
			return Paths{}, fmt.Errorf("meshid: stored identity in %s has no CA certificate — set %s", dir, EnvTLSCA)
		}
		slog.Info("meshid: using stored mesh identity", "dir", dir, "module", moduleID)
		return export(setenv, p)
	case certExists || keyExists:
		return Paths{}, fmt.Errorf("meshid: incomplete identity in %s (need both %s and %s); "+
			"remove the directory, run `muxcored enroll reset %s` and restart to re-enroll", dir, CertFile, KeyFile, moduleID)
	}

	// 3. Enroll.
	req, timeout, err := enrollParams(cfg, getenv, dir, moduleID, caFile)
	if err != nil {
		return Paths{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := enroll(ctx, req); err != nil {
		return Paths{}, err
	}
	p.CA = filepath.Join(dir, CAFile)
	if cfg.CAFile != "" || strings.TrimSpace(getenv(EnvTLSCA)) != "" {
		p.CA = caFile
	}
	p.Enrolled = true
	slog.Info("meshid: enrolled with core", "module", moduleID, "dir", dir)
	return export(setenv, p)
}

func export(setenv func(string, string) error, p Paths) (Paths, error) {
	for k, v := range map[string]string{EnvTLSCert: p.Cert, EnvTLSKey: p.Key, EnvTLSCA: p.CA} {
		if v == "" {
			continue
		}
		if err := setenv(k, v); err != nil {
			return Paths{}, fmt.Errorf("meshid: export %s: %w", k, err)
		}
	}
	return p, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// checkStored validates a stored identity: a parseable key pair whose
// certificate is for moduleID and not expired.
func checkStored(p Paths, moduleID string) error {
	pair, err := tls.LoadX509KeyPair(p.Cert, p.Key)
	if err != nil {
		return fmt.Errorf("meshid: stored identity: %w", err)
	}
	leaf := pair.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(pair.Certificate[0]); err != nil {
			return fmt.Errorf("meshid: stored identity: %w", err)
		}
	}
	if moduleID != "" && leaf.Subject.CommonName != moduleID {
		return fmt.Errorf("meshid: stored certificate %s is for %q, not %q", p.Cert, leaf.Subject.CommonName, moduleID)
	}
	if time.Now().After(leaf.NotAfter) {
		return fmt.Errorf("meshid: stored certificate %s expired on %s — remove it, run `muxcored enroll reset %s` and re-enroll",
			p.Cert, leaf.NotAfter.Format(time.RFC3339), leaf.Subject.CommonName)
	}
	return nil
}

// enrollParams collects and checks what enrollment needs.
func enrollParams(cfg Config, getenv func(string) string, dir, moduleID, caFile string) (enrollReq, time.Duration, error) {
	pick := func(v, env string) string {
		if v != "" {
			return v
		}
		return strings.TrimSpace(getenv(env))
	}
	token := pick(cfg.Token, EnvBootstrapToken)
	if token == "" {
		return enrollReq{}, 0, fmt.Errorf("meshid: %w: no certificate in %s and %s is not set — "+
			"run `muxcored enroll token <id>` and pass the token, or set %s/%s (dev: %s=dev with %s=true)",
			ErrNoIdentity, dir, EnvBootstrapToken, EnvTLSCert, EnvTLSKey, envProfile, envInsecure)
	}
	if caFile == "" {
		return enrollReq{}, 0, fmt.Errorf("meshid: enrollment needs the core CA to verify core — set %s or %s", EnvTLSCA, EnvCAExportDir)
	}
	if moduleID == "" {
		return enrollReq{}, 0, fmt.Errorf("meshid: enrollment needs the module ID (%s)", EnvModuleID)
	}
	addr := pick(cfg.GRPCAddr, EnvGRPCAddr)
	if addr == "" {
		return enrollReq{}, 0, fmt.Errorf("meshid: enrollment needs the core address (%s)", EnvGRPCAddr)
	}
	dns := cfg.DNSNames
	if len(dns) == 0 {
		for _, n := range strings.Split(getenv(EnvEnrollDNSNames), ",") {
			if n = strings.TrimSpace(n); n != "" {
				dns = append(dns, n)
			}
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return enrollReq{
		addr: addr, serverName: strings.TrimSpace(getenv(EnvTLSServerName)),
		moduleID: moduleID, token: token, caFile: caFile, dir: dir, dns: dns,
	}, timeout, nil
}

type enrollReq struct {
	addr, serverName, moduleID, token, caFile, dir string
	dns                                            []string
}

func enroll(ctx context.Context, r enrollReq) error {
	caPEM, err := os.ReadFile(r.caFile) //nolint:gosec // operator-configured CA path
	if err != nil {
		return fmt.Errorf("meshid: read core CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("meshid: no certificates in %s", r.caFile)
	}
	serverName := r.serverName
	if serverName == "" {
		if host, _, splitErr := net.SplitHostPort(r.addr); splitErr == nil {
			serverName = host
		}
	}
	// Server-verified TLS without a client certificate: the token is the
	// credential (BootstrapRegister is open in the auth interceptor).
	creds := credentials.NewTLS(&tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12})
	conn, err := grpc.NewClient(r.addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return fmt.Errorf("meshid: dial core %s: %w", r.addr, err)
	}
	defer func() { _ = conn.Close() }()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("meshid: generate key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: r.moduleID},
		DNSNames: r.dns,
	}, key)
	if err != nil {
		return fmt.Errorf("meshid: create CSR: %w", err)
	}
	req := &modulev1.BootstrapRegisterRequest{
		Token:    r.token,
		ModuleId: r.moduleID,
		CsrPem:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
		DnsNames: r.dns,
	}

	client := modulev1.NewModuleRegistrationClient(conn)
	var resp *modulev1.BootstrapRegisterResponse
	backoff := 500 * time.Millisecond
	for {
		resp, err = client.BootstrapRegister(ctx, req)
		if err == nil {
			break
		}
		// Keep trying while core is starting; anything else is final.
		if status.Code(err) != codes.Unavailable || ctx.Err() != nil {
			return fmt.Errorf("meshid: enroll with core %s: %w", r.addr, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("meshid: enroll with core %s: %w", r.addr, err)
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
	if !resp.GetAccepted() {
		return fmt.Errorf("meshid: core rejected enrollment of %q: %s", r.moduleID, resp.GetError())
	}
	if resp.GetKeyPem() != "" {
		return errors.New("meshid: core returned a private key for a CSR enrollment; refusing it")
	}
	certPEM := []byte(resp.GetSignedCert())
	if verr := verifyIssued(certPEM, key, r.moduleID, roots); verr != nil {
		return verr
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("meshid: encode key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	// Store the CA we pinned, not the one in the response.
	return store(r.dir, certPEM, keyPEM, caPEM)
}

// verifyIssued checks that the issued certificate is for our key and
// module ID and chains to the core CA we trust.
func verifyIssued(certPEM []byte, key *ecdsa.PrivateKey, moduleID string, roots *x509.CertPool) error {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("meshid: core returned no certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("meshid: parse issued certificate: %w", err)
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return errors.New("meshid: issued certificate is not for this module's key")
	}
	if leaf.Subject.CommonName != moduleID {
		return fmt.Errorf("meshid: issued certificate CN %q, want %q", leaf.Subject.CommonName, moduleID)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("meshid: issued certificate does not verify against the core CA: %w", err)
	}
	return nil
}

// store writes the identity with 0600 files in a 0700 directory. The
// certificate is installed last: its presence marks a complete identity.
func store(dir string, certPEM, keyPEM, caPEM []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("meshid: create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // directory: owner rwx only
		return fmt.Errorf("meshid: chmod %s: %w", dir, err)
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{CAFile, caPEM}, {KeyFile, keyPEM}, {CertFile, certPEM}} {
		if err := writeAtomic(filepath.Join(dir, f.name), f.data); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*") // created 0600
	if err != nil {
		return fmt.Errorf("meshid: write %s: %w", path, err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o600)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("meshid: write %s: %w", path, werr)
	}
	return nil
}
