package peerid

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func withAuth(ai credentials.AuthInfo) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: ai})
}

func TestVerifiedModuleID(t *testing.T) {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: " auth-local "}}
	verified := credentials.TLSInfo{State: tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf}},
	}}
	if id, ok := VerifiedModuleID(withAuth(verified)); !ok || id != "auth-local" {
		t.Fatalf("value TLSInfo: %q %v", id, ok)
	}
	if id, ok := VerifiedModuleID(withAuth(&verified)); !ok || id != "auth-local" {
		t.Fatalf("pointer TLSInfo: %q %v", id, ok)
	}
	unverified := credentials.TLSInfo{State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}}
	if _, ok := VerifiedModuleID(withAuth(unverified)); ok {
		t.Fatal("unverified chain accepted")
	}
	empty := &x509.Certificate{}
	noCN := credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{empty}}}}
	if _, ok := VerifiedModuleID(withAuth(noCN)); ok {
		t.Fatal("empty CN accepted")
	}
	if _, ok := VerifiedModuleID(context.Background()); ok {
		t.Fatal("no peer accepted")
	}
	var nilInfo *credentials.TLSInfo
	if IsTLS(withAuth(nilInfo)) {
		t.Fatal("nil *TLSInfo reported as TLS")
	}
	if !IsTLS(withAuth(unverified)) {
		t.Fatal("TLSInfo not reported as TLS")
	}
}
