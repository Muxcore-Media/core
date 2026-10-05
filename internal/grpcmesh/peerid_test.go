package grpcmesh

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/emptypb"
)

// testCA creates a core CertAuthority in a temp dir.
func testCA(t *testing.T) *CertAuthority {
	t.Helper()
	ca, err := NewCertAuthority(t.TempDir())
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	t.Cleanup(ca.Close)
	return ca
}

func caPool(t *testing.T, ca *CertAuthority) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CACertPEM()) {
		t.Fatal("append CA PEM")
	}
	return pool
}

// moduleTLSCert issues a CA-signed certificate for moduleID.
func moduleTLSCert(t *testing.T, ca *CertAuthority, moduleID string) tls.Certificate {
	t.Helper()
	certPEM, keyPEM, err := ca.IssueModuleCert(moduleID,
		[]net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost"})
	if err != nil {
		t.Fatalf("IssueModuleCert: %v", err)
	}
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return c
}

// selfSignedTLSCert returns a self-signed client certificate (not from the core CA).
func selfSignedTLSCert(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// serverTLSConfig returns a core-like server TLS config: CA-signed server
// cert, client certs verified against the core CA when given.
func serverTLSConfig(t *testing.T, ca *CertAuthority, clientAuth tls.ClientAuthType) *tls.Config {
	t.Helper()
	return &tls.Config{
		Certificates: []tls.Certificate{moduleTLSCert(t, ca, "muxcored")},
		ClientAuth:   clientAuth,
		ClientCAs:    caPool(t, ca),
		MinVersion:   tls.VersionTLS12,
	}
}

func clientTLSCreds(t *testing.T, ca *CertAuthority, cert *tls.Certificate) credentials.TransportCredentials {
	t.Helper()
	cfg := &tls.Config{
		RootCAs:    caPool(t, ca),
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	}
	if cert != nil {
		// Always present the certificate, even when it is not issued by one
		// of the server's acceptable CAs (Go would otherwise omit it).
		c := *cert
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &c, nil
		}
	}
	return credentials.NewTLS(cfg)
}

// probeVerifiedID runs a real TLS handshake and returns VerifiedModuleID as
// seen by the server handler.
func probeVerifiedID(t *testing.T, srvCfg *tls.Config, clientCreds credentials.TransportCredentials) (string, bool, error) {
	t.Helper()
	id, ok, _, err := probePeer(t, srvCfg, clientCreds)
	return id, ok, err
}

// probePeer is probeVerifiedID plus the number of peer certificates seen.
func probePeer(t *testing.T, srvCfg *tls.Config, clientCreds credentials.TransportCredentials) (string, bool, int, error) {
	t.Helper()
	type result struct {
		id        string
		ok        bool
		peerCerts int
	}
	got := make(chan result, 1)
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(srvCfg)),
		grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			id, ok := VerifiedModuleID(stream.Context())
			n := 0
			if p, has := peer.FromContext(stream.Context()); has {
				if ti, isTLS := p.AuthInfo.(credentials.TLSInfo); isTLS {
					n = len(ti.State.PeerCertificates)
				}
			}
			got <- result{id, ok, n}
			var in emptypb.Empty
			if err := stream.RecvMsg(&in); err != nil {
				return err
			}
			return stream.SendMsg(&emptypb.Empty{})
		}),
	)
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(clientCreds))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Invoke(ctx, "/probe.v1.Probe/Who", &emptypb.Empty{}, &emptypb.Empty{}); err != nil {
		return "", false, 0, err
	}
	r := <-got
	return r.id, r.ok, r.peerCerts, nil
}

func TestVerifiedModuleID_Handshake(t *testing.T) {
	ca := testCA(t)

	t.Run("CA-signed client cert yields CN", func(t *testing.T) {
		cert := moduleTLSCert(t, ca, "media-movies")
		id, ok, err := probeVerifiedID(t, serverTLSConfig(t, ca, tls.VerifyClientCertIfGiven), clientTLSCreds(t, ca, &cert))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if !ok || id != "media-movies" {
			t.Fatalf("VerifiedModuleID = %q, %v; want media-movies, true", id, ok)
		}
	})

	t.Run("TLS without client cert", func(t *testing.T) {
		id, ok, err := probeVerifiedID(t, serverTLSConfig(t, ca, tls.VerifyClientCertIfGiven), clientTLSCreds(t, ca, nil))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if ok || id != "" {
			t.Fatalf("VerifiedModuleID = %q, %v; want false", id, ok)
		}
	})

	t.Run("self-signed cert accepted unverified yields false", func(t *testing.T) {
		// RequestClientCert accepts any certificate without verifying it, so
		// PeerCertificates is populated but VerifiedChains is empty.
		cert := selfSignedTLSCert(t, "auth-local")
		id, ok, n, err := probePeer(t, serverTLSConfig(t, ca, tls.RequestClientCert), clientTLSCreds(t, ca, &cert))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if n != 1 {
			t.Fatalf("server saw %d peer certificates, want 1", n)
		}
		if ok || id != "" {
			t.Fatalf("VerifiedModuleID = %q, %v; want false for unverified cert", id, ok)
		}
	})

	t.Run("self-signed cert rejected by verifying server", func(t *testing.T) {
		cert := selfSignedTLSCert(t, "auth-local")
		_, _, err := probeVerifiedID(t, serverTLSConfig(t, ca, tls.VerifyClientCertIfGiven), clientTLSCreds(t, ca, &cert))
		if err == nil {
			t.Fatal("expected handshake failure for a certificate not signed by the core CA")
		}
	})
}

func TestVerifiedModuleID_NoTLS(t *testing.T) {
	if _, ok := VerifiedModuleID(context.Background()); ok {
		t.Fatal("no peer: want false")
	}
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr:     &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1},
		AuthInfo: plainAuthInfo{},
	})
	if _, ok := VerifiedModuleID(ctx); ok {
		t.Fatal("insecure AuthInfo: want false")
	}
	if peerIsTLS(ctx) {
		t.Fatal("insecure AuthInfo reported as TLS")
	}
}

func TestVerifiedModuleID_PeerCertWithoutChain(t *testing.T) {
	// A peer certificate alone (no verified chain) must not be trusted.
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: "auth-local"}}},
		}},
	})
	if id, ok := VerifiedModuleID(ctx); ok {
		t.Fatalf("got %q, want false", id)
	}
	if !peerIsTLS(ctx) {
		t.Fatal("TLSInfo not reported as TLS")
	}
}

type plainAuthInfo struct{}

func (plainAuthInfo) AuthType() string { return "insecure" }
