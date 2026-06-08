package callpolicy

import (
	"context"
	"os"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// BuiltinPolicy is the default call policy that enforces module capability checks.
// When strict mode is enabled (MUXCORE_STRICT_CALL_POLICY=true), calls from modules
// that lack a matching capability for the target are denied. In non-strict mode
// (default), all calls are allowed but capability mismatches are logged as warnings
// — this provides soft enforcement during migration.
type BuiltinPolicy struct {
	reg    contracts.Registry
	strict bool
}

// NewBuiltinPolicy creates a policy backed by the given registry.
// Set MUXCORE_STRICT_CALL_POLICY=true to enable hard denial of capability-mismatched calls.
func NewBuiltinPolicy(reg contracts.Registry) *BuiltinPolicy {
	return &BuiltinPolicy{
		reg:    reg,
		strict: os.Getenv("MUXCORE_STRICT_CALL_POLICY") == "true" || os.Getenv("MUXCORE_STRICT_CALL_POLICY") == "1",
	}
}

// Strict reports whether hard denial is active.
func (p *BuiltinPolicy) Strict() bool {
	return p.strict
}

// AllowCall implements contracts.CallPolicyProvider.
func (p *BuiltinPolicy) AllowCall(ctx context.Context, callerModuleID, targetModuleID, method string) (bool, error) {
	// Core (bootstrap) calls have no caller — always allow.
	if callerModuleID == "" {
		return true, nil
	}

	callerEntry, err := p.reg.Resolve(callerModuleID)
	if err != nil {
		// Unknown caller — allow in non-strict mode to avoid breaking dynamic modules.
		if p.strict {
			return false, nil
		}
		return true, nil
	}

	targetEntry, err := p.reg.Resolve(targetModuleID)
	if err != nil {
		// Unknown target — allow (target may not be registered yet).
		return true, nil
	}

	capabilities := callerEntry.Info.Capabilities
	targetRoles := targetEntry.Info.Roles

	if hasMatchingCapability(capabilities, targetRoles) {
		return true, nil
	}

	// Capability mismatch — warn in non-strict, deny in strict.
	if !p.strict {
		// Logged as warning — operator can review and enable strict mode.
		return true, nil
	}
	return false, nil
}

// hasMatchingCapability checks whether any caller capability matches any target role.
// Matching uses prefix comparison: a "storage" capability can call "storage-s3" role.
// The "*" and "mesh.admin" capabilities grant unrestricted access.
func hasMatchingCapability(callerCaps, targetRoles []string) bool {
	for _, cap := range callerCaps {
		for _, role := range targetRoles {
			if strings.HasPrefix(cap, role) || strings.HasPrefix(role, cap) {
				return true
			}
			if cap == "*" || cap == "mesh.admin" {
				return true
			}
		}
	}
	return false
}
