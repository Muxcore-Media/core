package contracts

import (
	"context"
	"time"
)

// EventStoreEntry is a persisted event with its stream position.
type EventStoreEntry struct {
	// Stream is the logical partition this event belongs to.
	Stream string

	// Sequence is the monotonically increasing position within the stream.
	// The first event in a stream has sequence 1.
	Sequence int64

	// Event is the stored event with type, source, payload, and metadata.
	Event Event

	// StoredAt is when the event was appended to the store.
	StoredAt time.Time

	// Extra carries provider-specific extensions (e.g., partition info,
	// checksum, storage tier).
	Extra map[string]any
}

// EventStore provides a durable, append-only event log. This is distinct
// from EventBus which handles fire-and-forget pub/sub. EventStore is for
// event sourcing: state is reconstructed by replaying all events in a
// stream from the beginning.
//
// Streams are logical partitions — typically one per aggregate or entity
// type ("user-abc123", "order-456", "module.metrics"). Append guarantees
// that events within a stream are ordered by sequence number.
//
// This contract is provider-agnostic: a module that calls Append/Read
// works identically whether the backend is PostgreSQL, Kafka, NATS
// JetStream, an in-memory log, or a future event store.
//
// If no EventStore is registered, Append is a no-op (returns nil sequence)
// and Read/Subscribe return empty — modules that only need pub/sub use
// EventBus; modules that need event sourcing require an EventStore.
type EventStore interface {
	// Append writes events to a stream and returns their assigned sequence
	// numbers. Events in a single Append call are atomic — either all
	// are written or none are. Returns the starting sequence number for
	// the batch and an error if the write fails.
	//
	// The first event in a new stream gets sequence 1.
	Append(ctx context.Context, stream string, events []Event) (firstSequence int64, err error)

	// Read returns events from a stream starting at fromSequence (inclusive).
	// limit caps the number of events returned. Pass 0 for no limit
	// (provider default). Returns events in ascending sequence order.
	// Returns an empty slice if fromSequence is beyond the stream end.
	Read(ctx context.Context, stream string, fromSequence int64, limit int) ([]EventStoreEntry, error)

	// Subscribe returns a channel that receives new events appended to
	// the stream starting from fromSequence. The channel closes when ctx
	// is cancelled. Events are delivered in sequence order.
	//
	// To catch up on existing events before live ones, call Read first
	// to get historical events, then Subscribe from the last sequence + 1.
	Subscribe(ctx context.Context, stream string, fromSequence int64) (<-chan EventStoreEntry, error)

	// Streams returns the names of all known streams.
	Streams(ctx context.Context) ([]string, error)
}
