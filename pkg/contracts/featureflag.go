package contracts

import "context"

// FeatureFlagProvider evaluates feature flags for gradual rollouts,
// kill switches, and A/B testing. Modules call IsEnabled before
// activating new code paths; the provider evaluates the flag against
// the caller's identity (via context) and any configured rules.
//
// This contract is provider-agnostic: a module that calls IsEnabled
// works identically whether the backend is a config file, LaunchDarkly,
// a database table, or a future feature flag service.
//
// If no FeatureFlagProvider is registered, IsEnabled returns defaultValue
// and GetVariant returns defaultValue — modules degrade gracefully
// without nil checks.
type FeatureFlagProvider interface {
	// IsEnabled checks whether a feature flag is enabled for the caller
	// in the given context. The context carries identity for percentage-based
	// rollouts and targeting rules. defaultValue is returned when no provider
	// is registered or the flag is not defined.
	IsEnabled(ctx context.Context, flag string, defaultValue bool) bool

	// GetVariant returns the assigned variant for a multivariate flag.
	// Useful for A/B testing where a flag has more than two states.
	// defaultValue is returned when no provider is registered or the
	// flag is not defined.
	GetVariant(ctx context.Context, flag string, defaultValue string) string
}
