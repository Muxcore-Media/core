package grpcmesh

import (
	"context"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// VerifiedModuleID returns the module ID of the gRPC peer: the Common Name of
// a client certificate that the TLS handshake verified against the server's
// client CA pool (the core CA). It returns false when the connection is not
// TLS, when the client presented no certificate, or when the certificate was
// not verified (e.g. tls.RequestClientCert, which accepts any certificate
// without building a chain). ADR-0017 decision 1.
func VerifiedModuleID(ctx context.Context) (string, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return "", false
	}
	var state *credentials.TLSInfo
	switch ai := p.AuthInfo.(type) {
	case credentials.TLSInfo:
		state = &ai
	case *credentials.TLSInfo:
		state = ai
	default:
		return "", false
	}
	if state == nil || len(state.State.VerifiedChains) == 0 || len(state.State.VerifiedChains[0]) == 0 {
		return "", false
	}
	// The leaf of a verified chain is the presented peer certificate.
	cn := strings.TrimSpace(state.State.VerifiedChains[0][0].Subject.CommonName)
	if cn == "" {
		return "", false
	}
	return cn, true
}

// peerIsTLS reports whether the gRPC peer connection uses TLS.
func peerIsTLS(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return false
	}
	switch p.AuthInfo.(type) {
	case credentials.TLSInfo, *credentials.TLSInfo:
		return true
	}
	return false
}
