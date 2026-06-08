package contracts

import "context"

// Credentials carries authentication material to the AuthProvider.
// Type identifies the credential scheme (e.g. "password", "token", "cert")
// and Data holds the opaque credential payload.
type Credentials struct {
	Type string
	Data []byte
}

type AuthProvider interface {
	Authenticate(ctx context.Context, credentials Credentials) (Session, error)
	Validate(ctx context.Context, token string) (Session, error)
	Revoke(ctx context.Context, token string) error
}

type Session struct {
	UserID      string
	Username    string
	Roles       []string
	Permissions []string
	Token       string
}

type Authorizer interface {
	Can(ctx context.Context, session Session, action string, resource string) (bool, error)
}
