package main

import (
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/profile"
	"google.golang.org/grpc/credentials"
)

// envTLSServerSANs lists extra DNS names / IPs for core's auto-issued server
// certificate (comma-separated), e.g. the compose service name modules dial.
const envTLSServerSANs = "MUXCORE_TLS_SERVER_SANS"

// meshSecurity is core's resolved transport security (ADR-0016).
type meshSecurity struct {
	// serverCreds are the gRPC server credentials; nil = plaintext (dev
	// profile with the insecure flag and no certificates configured).
	serverCreds credentials.TransportCredentials
	// certAuth is the core CA; nil when no CA is in use.
	certAuth *grpcmesh.CertAuthority
	// sidecar are the client credentials for dials to sidecar providers;
	// nil = plaintext (localhost only).
	sidecar *grpcmesh.SidecarClientTLS
	// caDir is the core CA directory ("" without a CA).
	caDir string
	// autoCert reports whether core issued its own server certificate.
	autoCert bool
}

// resolveProfile resolves the security profile, logs how it was resolved and
// any warnings, and prints the dev banner. A resolution error (unknown value,
// insecure flag in household) is returned for the caller to treat as fatal.
func resolveProfile() (profile.Resolved, error) {
	prof, err := profile.FromEnv()
	if err != nil {
		return prof, err
	}
	for _, w := range prof.Warnings {
		slog.Warn(w)
	}
	slog.Info("security profile resolved",
		"profile", string(prof.Name),
		"resolved_from", string(prof.Source),
		"insecure", prof.Insecure,
		"value", prof.Raw,
	)
	if prof.IsDev() {
		fmt.Fprintln(os.Stderr, profile.Banner)
		slog.Warn("DEV PROFILE: insecure operation is allowed — do not use for real data",
			"insecure", prof.Insecure)
	}
	return prof, nil
}

// resolveCADir returns the core CA directory: grpc.ca_cert_dir, else
// <data dir>/ca. An existing CA in the legacy ~/.muxcore/ca location is kept
// (with a warning) so upgrading does not rotate the CA under running modules.
func resolveCADir(cfg *config.Config, getenv func(string) string) string {
	if cfg.GRPC.CACertDir != "" {
		return cfg.GRPC.CACertDir
	}
	caDir := filepath.Join(profile.DataDir(getenv), "ca")
	if _, err := os.Stat(filepath.Join(caDir, "ca.crt")); err == nil {
		return caDir
	}
	if home, _ := os.UserHomeDir(); home != "" {
		legacy := filepath.Join(home, ".muxcore", "ca")
		if _, err := os.Stat(filepath.Join(legacy, "ca.crt")); err == nil {
			slog.Warn("using existing core CA in legacy location; move it to the data dir or set MUXCORE_GRPC_CA_CERT_DIR",
				"legacy_dir", legacy, "default_dir", caDir)
			return legacy
		}
	}
	return caDir
}

// serverCertSANs returns the SANs for core's auto-issued server certificate:
// loopback, localhost, the conventional service names, the host name, the
// host of the gRPC listen address, and MUXCORE_TLS_SERVER_SANS.
func serverCertSANs(cfg *config.Config, getenv func(string) string) ([]net.IP, []string) {
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	dns := []string{"localhost", "muxcored", "muxcore"}
	seen := map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, "muxcored": true, "muxcore": true}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		if ip := net.ParseIP(s); ip != nil {
			ips = append(ips, ip)
			return
		}
		dns = append(dns, s)
	}
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	if host, _, err := net.SplitHostPort(cfg.GRPC.Addr); err == nil {
		add(host)
	}
	for _, s := range strings.Split(getenv(envTLSServerSANs), ",") {
		add(s)
	}
	return ips, dns
}

// setupMeshSecurity creates the core CA, server credentials and sidecar
// client credentials for the resolved profile:
//
//   - TLS is on unless the dev profile's insecure flag is set and no
//     certificate or mTLS is configured.
//   - The core CA is created whenever TLS is on in household, whenever core
//     must issue its own server certificate, with mtls_enabled, or when
//     grpc.ca_cert_dir is set.
//   - Client certificates are verified against the core CA (+ configured CA)
//     when presented (VerifyClientCertIfGiven); mtls_enabled keeps
//     RequireAndVerifyClientCert.
//
// cfg.GRPC.CertFile/KeyFile (and the HTTP cert when unset) are filled in when
// core issues its own certificate.
func setupMeshSecurity(cfg *config.Config, prof profile.Resolved, getenv func(string) string) (*meshSecurity, error) { //nolint:gocyclo // one branch per TLS configuration case
	if prof.IsHousehold() && prof.Insecure {
		return nil, profile.ErrInsecureInHousehold
	}
	sec := &meshSecurity{}
	explicitCerts := cfg.GRPC.CertFile != "" || cfg.GRPC.KeyFile != ""
	tlsOn := !prof.Insecure || explicitCerts || cfg.GRPC.MTLSEnabled
	needCA := cfg.GRPC.CACertDir != "" ||
		(tlsOn && (prof.IsHousehold() || !explicitCerts || cfg.GRPC.MTLSEnabled))

	if needCA {
		sec.caDir = resolveCADir(cfg, getenv)
		ca, err := grpcmesh.NewCertAuthority(sec.caDir)
		if err != nil {
			return nil, fmt.Errorf("certificate authority: %w", err)
		}
		sec.certAuth = ca
		slog.Info("certificate authority ready", "dir", sec.caDir)
		if dir := profile.CAExportDir(getenv); dir != "" {
			path, err := ca.ExportCACert(dir)
			if err != nil {
				return nil, fmt.Errorf("export CA certificate to %s: %w", profile.EnvCAExportDir, err)
			}
			slog.Info("core CA certificate exported", "path", path)
		}
	}

	if tlsOn && !explicitCerts {
		ips, dns := serverCertSANs(cfg, getenv)
		certPath, keyPath, err := sec.certAuth.IssueServerCertForDir("muxcored", filepath.Join(sec.caDir, "server"), ips, dns)
		if err != nil {
			return nil, fmt.Errorf("issue muxcored server cert: %w", err)
		}
		cfg.GRPC.CertFile = certPath
		cfg.GRPC.KeyFile = keyPath
		if cfg.GRPC.CACertFile == "" {
			cfg.GRPC.CACertFile = filepath.Join(sec.caDir, "ca.crt")
		}
		sec.autoCert = true
		slog.Info("auto-issued muxcored TLS certificate", "cert", certPath, "dns_sans", dns)
	}
	if tlsOn && cfg.Server.CertFile == "" && cfg.Server.KeyFile == "" {
		cfg.Server.CertFile = cfg.GRPC.CertFile
		cfg.Server.KeyFile = cfg.GRPC.KeyFile
	}

	if !tlsOn {
		slog.Warn("gRPC TLS is disabled — insecure mode (dev profile)")
		return sec, nil
	}

	var pool *x509.CertPool
	if sec.certAuth != nil {
		pool = sec.certAuth.CorePool()
	}
	if cfg.GRPC.CACertFile != "" {
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if err := grpcmesh.AppendCAFile(pool, cfg.GRPC.CACertFile); err != nil {
			return nil, fmt.Errorf("grpc CA: %w", err)
		}
	}
	if cfg.GRPC.MTLSEnabled && pool == nil {
		return nil, errors.New("mTLS is enabled but no CA certificate is available — set MUXCORE_GRPC_MTLS_CA or MUXCORE_GRPC_CA_CERT_DIR")
	}

	creds, err := grpcmesh.ServerTLSCredentials(cfg.GRPC.CertFile, cfg.GRPC.KeyFile, pool, cfg.GRPC.MTLSEnabled)
	if err != nil {
		return nil, fmt.Errorf("grpc tls: %w", err)
	}
	sec.serverCreds = creds

	sidecar, err := grpcmesh.NewSidecarClientTLS(pool, cfg.GRPC.CertFile, cfg.GRPC.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("sidecar client tls: %w", err)
	}
	sec.sidecar = sidecar

	clientAuth := "none"
	switch {
	case cfg.GRPC.MTLSEnabled:
		clientAuth = "require_and_verify"
	case pool != nil:
		clientAuth = "verify_if_given"
	}
	slog.Info("gRPC TLS enabled",
		"cert", cfg.GRPC.CertFile,
		"client_auth", clientAuth,
		"auto_cert", sec.autoCert,
		"profile", string(prof.Name),
	)
	return sec, nil
}
