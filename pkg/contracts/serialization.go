package contracts

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
// If no SerializationProvider is registered, modules handle
// serialization themselves — this is acceptable for simple deployments
// where all modules agree on a single format.
type SerializationProvider interface {
	// Marshal serializes v to bytes using the specified content type.
	// Common content types: "application/json", "application/x-protobuf",
	// "application/msgpack", "application/yaml".
	Marshal(contentType string, v any) ([]byte, error)

	// Unmarshal deserializes data into v using the specified content type.
	// v must be a pointer to the target type.
	Unmarshal(contentType string, data []byte, v any) error

	// SupportedTypes returns the content types this provider can handle.
	// Callers can negotiate format by intersecting their needs with this list.
	SupportedTypes() []string
}
