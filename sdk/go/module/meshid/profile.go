package meshid

import (
	"errors"
	"fmt"
	"strings"
)

// Profile environment (ADR-0016). Core is the only enforcer; the SDK reads
// the profile only to fail early with a clear message.
const (
	envProfile        = "MUXCORE_PROFILE"
	envInsecure       = "MUXCORE_INSECURE_DISABLE_TLS"
	envInsecureLegacy = "MUXCORE_DEV_TLS_SKIP"
)

// ErrInsecureInHousehold is returned when a module runs with the insecure
// flag while MUXCORE_PROFILE is household (or its alias staging).
var ErrInsecureInHousehold = errors.New("insecure mode is not allowed in the household profile")

// InsecureFromEnv reports whether MUXCORE_INSECURE_DISABLE_TLS (or the
// deprecated MUXCORE_DEV_TLS_SKIP) is true or 1.
func InsecureFromEnv(getenv func(string) string) bool {
	for _, k := range []string{envInsecure, envInsecureLegacy} {
		if v := strings.TrimSpace(getenv(k)); v == "true" || v == "1" {
			return true
		}
	}
	return false
}

// explicitHousehold reports whether MUXCORE_PROFILE names the household
// profile (or its alias staging). When the profile is unset, an insecure
// module resolves to dev (ADR-0016 phase 0), as it does in core.
func explicitHousehold(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(envProfile))) {
	case "household", "staging":
		return true
	}
	return false
}

// CheckProfile returns ErrInsecureInHousehold (wrapped with a hint) when
// insecure is requested in the household profile.
func CheckProfile(getenv func(string) string, insecure bool) error {
	if insecure && explicitHousehold(getenv) {
		return fmt.Errorf("%w: %s=%s but this module is configured for plaintext — "+
			"unset %s and give the module a mesh identity (%s), or set %s=dev for a development install",
			ErrInsecureInHousehold, envProfile, strings.TrimSpace(getenv(envProfile)),
			envInsecure, EnvBootstrapToken, envProfile)
	}
	return nil
}
