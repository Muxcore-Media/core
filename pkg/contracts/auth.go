package contracts

import "context"

type AuthProvider interface {
	Authenticate(ctx context.Context, credentials any) (Session, error)
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
