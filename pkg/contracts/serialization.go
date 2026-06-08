package contracts

// SafeSerializationTypes enumerates content types that are considered safe
// for deserialization. These formats do not support code-execution gadgets
// or type-confusion attacks in their standard Go implementations.
//
// Formats NOT in this list (e.g., "application/yaml", "application/gob",
// "application/x-java-serialized-object") MUST NOT be supported by
// SerializationProvider implementations unless explicit sandboxing is
// applied and documented.
const (
	SafeContentTypeJSON    = "application/json"
	SafeContentTypeProtobuf = "application/x-protobuf"
	SafeContentTypeMsgpack  = "application/msgpack"
)

// SerializationProvider handles marshaling and unmarshaling of data
// with content-type negotiation. Modules use this to serialize/deserialize
// payloads without knowing which format the other side expects.
//
// A module producing protobuf can call Marshal("application/json", v)
// and the provider handles the conversion. A module consuming JSON can
// call Unmarshal(contentType, data, v) regardless of the wire format.
//
// This contract is provider-agnostic: a module that calls Marshal/Unmarshal
// works identically whether the backend uses encoding/json + protobuf,
// msgpack, or a future serialization format.
//
// SECURITY: Unmarshal deserializes untrusted data into an arbitrary target
// type. Implementations MUST:
//   - Reject content types that support code execution (YAML v1, gob, etc.)
//     unless sandboxed with strict decoding modes.
//   - Enforce type safety — the target 'v' must be a concrete Go type,
//     and the implementation must not instantiate types based on the
//     serialized payload's type hints (no polymorphic deserialization).
//   - Reject payloads that exceed a reasonable size limit to prevent
//     memory-exhaustion DoS. A minimum of 10 MB per payload is recommended.
//
// If no SerializationProvider is registered, modules handle
// serialization themselves — this is acceptable for simple deployments
// where all modules agree on a single format.
type SerializationProvider interface {
	// Marshal serializes v to bytes using the specified content type.
	// Common content types: "application/json", "application/x-protobuf",
	// "application/msgpack".
	Marshal(contentType string, v any) ([]byte, error)

	// Unmarshal deserializes data into v using the specified content type.
	// v must be a pointer to a concrete target type.
	//
	// SECURITY: The content type and data originate from untrusted sources
	// (the wire or another module). Implementations MUST validate the
	// content type against a safelist before deserializing.
	Unmarshal(contentType string, data []byte, v any) error

	// SupportedTypes returns the content types this provider can handle.
	// Callers can negotiate format by intersecting their needs with this list.
	//
	// SECURITY: The returned list MUST only include types that have been
	// vetted for safe deserialization. See SafeSerializationTypes above.
	SupportedTypes() []string
}
