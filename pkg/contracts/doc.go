// Package contracts defines the stable interfaces between MuxCore and its
// modules. These interfaces form the v1 contract boundary.
//
// STABILITY GUARANTEE (v1)
//
// All exported types, interfaces, constants, and functions in this package
// are frozen for the v1 lifetime. Breaking changes — including but not limited
// to adding or removing interface methods, changing struct fields, or
// modifying constant values — require a v2 major version.
//
// The following are NOT covered by this guarantee:
//   - Types explicitly marked "RESERVED" or "Experimental" in their doc comment
//   - The `eventschema.go` file (reserved for future schema-evolution support)
//
// # IMPLEMENTING CONTRACTS
//
// Modules implement these interfaces and register with core via gRPC.
// Each contract has a corresponding capability string for runtime discovery
// (see capabilities.go). Modules do not import core binary — they only
// import this contracts package and the proto-generated Go types.
//
// # IMPORT PATH
//
// This package is released as a standalone Go module so that module authors
// can depend on it without pulling in the full core dependency tree:
//
//	require github.com/Muxcore-Media/core/pkg/contracts v1.0.0
//
// All feedback: https://github.com/Muxcore-Media/core/issues
package contracts
