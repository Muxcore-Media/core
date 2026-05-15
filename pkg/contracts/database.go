package contracts

import "context"

// DatabaseProvider is implemented by database modules (database-postgres, database-sqlite, etc.)
// to provide persistent storage for modules. Core defines the contract; modules provide the driver.
type DatabaseProvider interface {
	// Open initializes the database connection.
	Open(ctx context.Context, connString string) error
	// Close gracefully shuts down the database connection.
	Close(ctx context.Context) error
	// Health checks whether the database is reachable.
	Health(ctx context.Context) error
	// Exec runs a statement that modifies data (INSERT, UPDATE, DELETE, DDL).
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	// Query runs a read query and returns a row iterator.
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	// Transaction runs fn inside a database transaction, committing on success and rolling back on error.
	Transaction(ctx context.Context, fn func(Tx) error) error
	// Migrate applies ordered schema migrations. Implementations track which migrations
	// have already been applied and skip those.
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
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Migration represents a versioned schema migration.
type Migration struct {
	Version int
	Name    string
	Up      string // SQL to apply
	Down    string // SQL to roll back
}
