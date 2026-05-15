package contracts

import "context"

// BackupInfo describes a completed backup.
type BackupInfo struct {
	ID        string
	Timestamp int64
	Size      int64
	Modules   []string // module IDs included in the backup
}

// BackupProvider is implemented by backup modules (backup-local, backup-s3, etc.)
type BackupProvider interface {
	// CreateBackup captures state from all registered Backupable modules.
	CreateBackup(ctx context.Context) (BackupInfo, error)
	// Restore restores state from a previous backup.
	Restore(ctx context.Context, backupID string) error
	// ListBackups returns all available backups.
	ListBackups(ctx context.Context) ([]BackupInfo, error)
	// DeleteBackup removes a stored backup.
	DeleteBackup(ctx context.Context, backupID string) error
}

// Backupable is implemented by modules that have state to back up.
type Backupable interface {
	// ExportState returns serialized state for backup.
	ExportState(ctx context.Context) ([]byte, error)
	// ImportState restores state from a backup.
	ImportState(ctx context.Context, data []byte) error
}
