package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Environment read by TransportCredentialsFromEnv.
const (
	EnvTLSCert       = "MUXCORE_TLS_CERT"
	EnvTLSKey        = "MUXCORE_TLS_KEY"
	EnvTLSCA         = "MUXCORE_TLS_CA"
	EnvTLSServerName = "MUXCORE_TLS_SERVER_NAME"
	envInsecure      = "MUXCORE_INSECURE_DISABLE_TLS"
	envInsecureOld   = "MUXCORE_DEV_TLS_SKIP"
	envProfile       = "MUXCORE_PROFILE"
)

// ErrInsecureInHousehold is returned by Dial when WithInsecure is used while
// MUXCORE_PROFILE is household (or staging).
var ErrInsecureInHousehold = errors.New("insecure mode is not allowed in the household profile — " +
	"remove WithInsecure / MUXCORE_INSECURE_DISABLE_TLS and use the module's mesh identity, or set MUXCORE_PROFILE=dev")

// TransportCredentialsFromEnv builds client transport credentials from
// MUXCORE_TLS_CA (roots for verifying core), MUXCORE_TLS_CERT and
// MUXCORE_TLS_KEY (client certificate, both or neither) and
// MUXCORE_TLS_SERVER_NAME. When none of the TLS variables is set it returns
// plaintext credentials if MUXCORE_INSECURE_DISABLE_TLS is true (outside
// the household profile), and nil otherwise.
func TransportCredentialsFromEnv(getenv func(string) string) (credentials.TransportCredentials, error) {
	certFile := strings.TrimSpace(getenv(EnvTLSCert))
	keyFile := strings.TrimSpace(getenv(EnvTLSKey))
	caFile := strings.TrimSpace(getenv(EnvTLSCA))
	if certFile == "" && keyFile == "" && caFile == "" {
		if insecureFlag(getenv) {
			if householdProfile(getenv) {
				return nil, ErrInsecureInHousehold
			}
			return insecure.NewCredentials(), nil
		}
		return nil, nil
	}
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("%s and %s must be set together", EnvTLSCert, EnvTLSKey)
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: strings.TrimSpace(getenv(EnvTLSServerName)),
	}
	if certFile != "" {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load %s/%s: %w", EnvTLSCert, EnvTLSKey, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if caFile != "" {
		pemData, err := os.ReadFile(caFile) //nolint:gosec // operator-configured CA path
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", EnvTLSCA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("no certificates in %s (%s)", EnvTLSCA, caFile)
		}
		cfg.RootCAs = pool
	}
	return credentials.NewTLS(cfg), nil
}

func insecureFlag(getenv func(string) string) bool {
	for _, k := range []string{envInsecure, envInsecureOld} {
		if v := strings.TrimSpace(getenv(k)); v == "true" || v == "1" {
			return true
		}
	}
	return false
}

// householdProfile reports whether MUXCORE_PROFILE explicitly names the
// household profile (or staging). An unset profile with plaintext resolves
// to dev (ADR-0016 phase 0), so it is not an error here.
func householdProfile(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(envProfile))) {
	case "household", "staging":
		return true
	}
	return false
}
