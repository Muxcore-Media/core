//go:build !unix

package enroll

// lockFile is a no-op where advisory file locks are unavailable; the
// in-process mutex still serialises core's own ledger updates.
func lockFile(string) (func(), error) { return func() {}, nil }
