package contracts

import "context"

// ValidationResult carries the outcome of an input validation call.
// Modules use this to decide whether to accept, reject, or sanitize
// incoming data before processing.
type ValidationResult struct {
	// Valid is true if the input passed all schema checks without errors.
	// When Valid is true, Sanitized holds the (possibly cleaned) data
	// that should be used in place of the original.
	Valid bool

	// Sanitized is the cleaned/sanitized version of the input data.
	// When Valid is true, callers should use this instead of the raw input.
	// When Valid is false, Sanitized may be nil or partial.
	Sanitized []byte

	// Errors is the list of validation failures. Empty when Valid is true.
	// Each entry describes a specific failure (e.g., "field email must
	// match pattern ^[^@]+@[^@]+$").
	Errors []string
}

// InputValidator performs centralized input validation for the MuxCore fabric.
// Modules implement this contract to provide schema-aware validation that other
// modules can delegate to, rather than duplicating validation logic.
//
// Schema conventions:
//
//	"json:<schema-name>"   — JSON Schema validation (e.g., "json:user-create")
//	"regex:<pattern>"      — Regex-based validation (e.g., "regex:^[a-z]+$")
//	"sql:<param-count>"    — SQL injection-safe parameter binding (e.g., "sql:3")
//
// Modules discover registered validators via the registry and call SupportedSchemas()
// to check which schemas are available before calling Validate. If no InputValidator
// is registered, modules handle validation themselves (degrade gracefully).
//
// Discovered via FindByCapability("input.validate"). If multiple validators
// register, the first found is consulted. Validators may internally delegate
// to other validators for schemas they do not support.
type InputValidator interface {
	// Validate checks the given data against the named schema and returns
	// a result indicating pass/fail plus any sanitized output.
	//
	// The schema parameter follows the conventions above. If the schema is
	// not recognised, implementations should return a ValidationResult with
	// Valid=false and an error entry explaining the unknown schema, rather
	// than returning an error — this allows callers to fall back gracefully.
	Validate(ctx context.Context, data []byte, schema string) (ValidationResult, error)

	// SupportedSchemas returns the list of schema identifiers this validator
	// can handle. Used for capability discovery so callers can determine at
	// registration time whether a validator meets their needs.
	SupportedSchemas() []string
}
