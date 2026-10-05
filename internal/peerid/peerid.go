// Package peerid reads the verified module identity of a gRPC peer
// (ADR-0017 decision 1). It is a leaf package so that both the mesh
// (internal/grpcmesh) and the module manager (internal/module/mgr) can use it.
package peerid

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
// without building a chain).
func VerifiedModuleID(ctx context.Context) (string, bool) {
	state, ok := tlsInfo(ctx)
	if !ok || len(state.State.VerifiedChains) == 0 || len(state.State.VerifiedChains[0]) == 0 {
		return "", false
	}
	// The leaf of a verified chain is the presented peer certificate.
	cn := strings.TrimSpace(state.State.VerifiedChains[0][0].Subject.CommonName)
	if cn == "" {
		return "", false
	}
	return cn, true
}

// IsTLS reports whether the gRPC peer connection uses TLS.
func IsTLS(ctx context.Context) bool {
	_, ok := tlsInfo(ctx)
	return ok
}

func tlsInfo(ctx context.Context) (*credentials.TLSInfo, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return nil, false
	}
	switch ai := p.AuthInfo.(type) {
	case credentials.TLSInfo:
		return &ai, true
	case *credentials.TLSInfo:
		if ai == nil {
			return nil, false
		}
		return ai, true
	}
	return nil, false
}
