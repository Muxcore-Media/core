// Package erasure is the consumer side of the ADR-0035 user erasure ledger.
//
// Every module that owns personal data runs one Reconciler. It reads the
// erasure tombstones from the identity provider's ledger
// (AuthService.ListUserErasures), applies each tombstone the module has not
// applied yet through the module's Owner, checks the post-condition and
// acknowledges the outcome (AuthService.AckUserErasure). The module supplies
// only the Owner; discovery, the provider identity check, paging, sweep
// scheduling and acknowledgement live here so the security path is not copied
// into every module.
//
// # Trust model
//
//   - The ledger is the only authority. No event, header, payload or response
//     from any other source erases data; this package has no other input.
//   - The ledger is read only from the provider of the exclusive "identity"
//     capability as reported by core's DiscoveryService. More than one distinct
//     provider is an error (fail closed), never a choice.
//   - The provider is dialled with mesh mTLS: the chain must verify against
//     MUXCORE_TLS_CA (system roots are never used), the certificate must be
//     valid for the provider's module id as a DNS name, and the verified leaf
//     CN must EQUAL that module id (ADR-0033 §1 pattern). A same-CA
//     certificate for any other module is rejected even if it carries the
//     provider's name as a SAN. MUXCORE_TLS_SERVER_NAME is ignored and
//     InsecureSkipVerify is never used.
//   - The module presents its own enrolled certificate, whose CN must equal
//     Owner.ModuleID(): the provider attributes acknowledgements to the
//     verified CN, never to a request field.
//   - Plaintext is used only when the explicit insecure dev flag is set
//     (meshtls.Insecure: MUXCORE_INSECURE_DISABLE_TLS, MUXCORE_DEV_TLS_SKIP or
//     MUXCORE_GRPC_INSECURE), and is refused in the household and staging
//     profiles exactly as meshtls refuses it.
//
// # Owner contract
//
// Apply must erase or anonymise the tombstone's data and record the erasure
// id as applied in ONE local transaction: either both happen or neither does.
// The Reconciler calls Applied first and never calls Apply for a tombstone
// the owner reports as applied; it re-acknowledges instead. Apply must be
// keyed on Tombstone.UserID (never a username) and, where the owner stores a
// tenant, must check Tombstone.TenantID. An owner may implement Verifier to
// report rows still carrying the user id after application.
//
// The Reconciler never logs user ids, only erasure ids. Error text returned
// by an owner has the tombstone's user id redacted before it is logged.
package erasure

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// Tombstone is one erasure from the provider's ledger.
type Tombstone struct {
	// DeletedAt is the provider's deletion time (UTC).
	DeletedAt time.Time
	// ErasureID is the opaque, random id of the erasure. Record it in the
	// owner's erasure_applied table.
	ErasureID string
	// UserID is the erased user's id. It is globally unique and never reused
	// by the provider; erase by this id, never by username.
	UserID string
	// TenantID is the user's tenant; empty means the single household.
	TenantID string
}

// Counts are per-store numbers of rows erased or anonymised, reported to the
// provider with the acknowledgement, e.g. {"user_blobs": 3}. Keys must match
// [a-z0-9_.-]{1,64}; at most 32 entries are sent and negative values are
// dropped.
type Counts map[string]int64

// Owner is a module's personal-data store.
type Owner interface {
	// ModuleID is the module's id. It must equal the CN of the module's mesh
	// certificate, which is how the provider identifies the acknowledgement.
	ModuleID() string
	// Applied reports whether erasureID is in the owner's local record of
	// applied erasures.
	Applied(ctx context.Context, erasureID string) (bool, error)
	// Apply erases or anonymises the tombstone's data AND records
	// t.ErasureID as applied, in one local transaction. On error nothing may
	// have changed. Return ErrUnsupported (wrapped) if the owner cannot
	// apply erasures at all, or wrap the error with WithDetail to report a
	// specific detail code.
	Apply(ctx context.Context, t Tombstone) (Counts, error)
}

// Verifier is optionally implemented by an Owner to check the post-condition
// after application: the number of rows still carrying t.UserID that the
// disposition should have removed or anonymised. Anything but 0 is acknowledged
// as FAILED with detail code "postcondition_unmet".
type Verifier interface {
	Verify(ctx context.Context, t Tombstone) (remaining int, err error)
}

// ErrUnsupported is returned (wrapped) by Owner.Apply when the owner cannot
// apply erasures; the Reconciler acknowledges UNSUPPORTED.
var ErrUnsupported = errors.New("erasure: owner cannot apply erasures")

// Detail codes sent by the Reconciler.
const (
	DetailApplyFailed        = "apply_failed"
	DetailVerifyFailed       = "verify_error"
	DetailPostconditionUnmet = "postcondition_unmet"
	DetailUnsupported        = "owner_unsupported"
	DetailInvalidTombstone   = "invalid_tombstone"
)

var detailCodeRE = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// ValidDetailCode reports whether code is a valid detail code or counts key:
// 1 to 64 characters from [a-z0-9_.-].
func ValidDetailCode(code string) bool { return detailCodeRE.MatchString(code) }

type detailError struct {
	err  error
	code string
}

func (e *detailError) Error() string { return e.err.Error() }
func (e *detailError) Unwrap() error { return e.err }

// WithDetail wraps err so a failed Apply is acknowledged with detail code
// instead of "apply_failed". The code must be short and never contain
// personal data; an invalid code is replaced by "apply_failed".
func WithDetail(code string, err error) error {
	if err == nil {
		return nil
	}
	return &detailError{err: err, code: code}
}

func detailCode(err error) string {
	var de *detailError
	if errors.As(err, &de) && ValidDetailCode(de.code) {
		return de.code
	}
	return DetailApplyFailed
}
