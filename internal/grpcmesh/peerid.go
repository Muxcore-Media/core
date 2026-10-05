package grpcmesh

import (
	"context"

	"github.com/Muxcore-Media/core/internal/peerid"
)

// VerifiedModuleID returns the module ID of the gRPC peer: the Common Name of
// a client certificate that the TLS handshake verified against the server's
// client CA pool (the core CA). It returns false when the connection is not
// TLS, when the client presented no certificate, or when the certificate was
// not verified (e.g. tls.RequestClientCert, which accepts any certificate
// without building a chain). ADR-0017 decision 1. See peerid.VerifiedModuleID.
func VerifiedModuleID(ctx context.Context) (string, bool) {
	return peerid.VerifiedModuleID(ctx)
}

// peerIsTLS reports whether the gRPC peer connection uses TLS.
func peerIsTLS(ctx context.Context) bool {
	return peerid.IsTLS(ctx)
}
