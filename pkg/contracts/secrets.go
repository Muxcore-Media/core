package contracts

import "context"

// SecretsProvider manages sensitive configuration values (API keys, tokens,
// passwords). Modules implement this to back secrets from environment variables,
// HashiCorp Vault, encrypted files, or external secret managers.
//
// This contract exists so modules never read raw env vars for credentials.
// A module that needs a Jackett API key calls deps.Secrets.Get("jackett_api_key")
// instead of os.Getenv("JACKETT_API_KEY"). This enables audit logging, rotation,
// and centralized secret management without changing module code.
type SecretsProvider interface {
	// Get retrieves a secret by key. Returns an error if not found.
	Get(ctx context.Context, key string) (string, error)
	// Set stores a secret. Implementations may be read-only (e.g., env-backed)
	// and return an error for Set calls.
	Set(ctx context.Context, key, value string) error
	// Delete removes a secret. May not be supported by all backends.
	Delete(ctx context.Context, key string) error
	// List returns all known keys. Values are NOT included — only key names.
	List(ctx context.Context) ([]string, error)
}
