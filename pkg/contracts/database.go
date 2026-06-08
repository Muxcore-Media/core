package contracts

import (
	"context"
	"errors"
)

// ErrDatabaseCredentialExposure is a sentinel error that DatabaseProvider
// implementations SHOULD return if they detect that credentials were
// passed as a raw connection string. This enables CI lint rules to detect
// unsafe database initialization.
var ErrDatabaseCredentialExposure = errors.New("database: credential passed as raw string — use DatabaseParams instead")

// DatabaseParams separates connection parameters from credentials.
// Use this struct with DatabaseProvider.Open instead of passing a raw connection
// string. This prevents accidental credential exposure in logs,
// stack traces, and configuration serialization.
//
// SECURITY: The Password field is a plain string — implementations
// MUST NOT log DatabaseParams or any field within it. If the database
// driver supports credential-free authentication (IAM, workload identity,
// certificate-based auth), prefer those methods and leave Password empty.
type DatabaseParams struct {
	// Driver is the database driver name (e.g., "postgres", "sqlite3").
	Driver string
	// Host is the database server address (host:port or socket path).
	Host string
	// Database is the database name.
	Database string
	// User is the database username.
	User string
	// Password is the database password. Leave empty for passwordless auth.
	//
	// SECURITY: Never log this field. Fetch the password from
	// SecretsProvider at connection time rather than storing it in
	// application state.
	Password string
	// Extra carries driver-specific connection parameters (e.g., sslmode,
	// connect_timeout). These are appended to the DSN after the core params.
	// Keys and values in Extra must not contain credentials.
	Extra map[string]string
}

// DatabaseProvider is implemented by database modules (database-postgres, database-sqlite, etc.)
// to provide persistent storage for modules. Core defines the contract; modules provide the driver.
//
// Discovered via FindByCapability(CapabilityDatabase).
type DatabaseProvider interface {
	// Open initializes the database connection using separated connection
	// parameters. This isolates credentials from connection metadata,
	// reducing the risk of accidental credential exposure.
	//
	// Implementations SHOULD return ErrDatabaseCredentialExposure if
	// the caller attempts to embed credentials in Extra.
	Open(ctx context.Context, params DatabaseParams) error

	// Close gracefully shuts down the database connection.
	Close(ctx context.Context) error
	// Health checks whether the database is reachable.
	Health(ctx context.Context) error
	// Exec runs a statement that modifies data (INSERT, UPDATE, DELETE, DDL).
	//
	// SECURITY: Callers MUST use parameterized queries. Never concatenate
	// user input into the query string. The args ...any parameter supports
	// standard SQL placeholders ($1, ?, :name). Consider using
	// InputValidator with "sql:<param-count>" schema for additional
	// injection protection.
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	// Query runs a read query and returns a row iterator.
	//
	// SECURITY: Same parameterization requirement as Exec. All user
	// input MUST be passed via args, never interpolated into query.
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	// Transaction runs fn inside a database transaction, committing on success and rolling back on error.
	Transaction(ctx context.Context, fn func(Tx) error) error
	// Migrate applies ordered schema migrations. Implementations track which migrations
	// have already been applied and skip those.
	//
	// SECURITY: Migration SQL (Up/Down strings) is trusted schema code.
	// Implementations SHOULD verify migration content against a known
	// checksum or signature before execution when operating in production.
	Migrate(ctx context.Context, migrations []Migration) error
}

// Rows is an iterator over query result rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
}

// Tx is a database transaction handle.
type Tx interface {
	// SECURITY: Same SQL parameterization requirements as DatabaseProvider.Exec/Query.
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Migration represents a versioned schema migration.
type Migration struct {
	Version int
	Name    string
	Up      string // SQL to apply
	// Down is SQL to roll back.
	// RESERVED — DatabaseProvider.Migrate does not yet support rollback;
	// will be wired when Rollback is added.
	Down string
}
