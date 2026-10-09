# Event Bus

MuxCore's event bus is the primary communication channel between modules. All inter-module events flow through it. Core provides an in-memory implementation with optional write-ahead log (WAL) for persistence.

---

## Architecture

```
Publisher (module)
      │
      ▼
 PublishPolicyProvider.CanPublish()  ← capability check
      │
      ▼
  WAL.Write()  ← optional, if MUXCORE_EVENT_JOURNAL_PATH is set
      │
      ▼
 Per-subscriber channel (capacity 256)
      │           ├── subscriber-1 worker goroutine
      │           ├── subscriber-2 worker goroutine
      │           └── subscriber-N worker goroutine
```

The event bus is **deny-by-default**: if no `PublishPolicyProvider` is configured, every `Publish` call — core's own included — returns an error. Core has **no built-in publish policy**. At bootstrap `WirePublishPolicy` (`internal/bootstrap/wiring.go`) installs the provider of record of the `publish.policy` capability (an in-process module or a sidecar such as `publish-policy-default`); when no module provides it, nothing is installed and publishing stays denied. A policy that also implements `ResourcePublishPolicyProvider` is consulted with the full event; otherwise `CanPublish(caller, eventType)` is used.

### Delivery guarantees

Delivery is **at-most-once**. There is no acknowledgement, redelivery or dead-letter queue:

- an event is dropped for a subscriber whose channel is full, and `Publish` still returns `nil`;
- a handler error or timeout is logged and the event is not retried;
- a subscriber that is not subscribed when the event is published (down, restarting, or a remote module whose `EventService/Subscribe` stream has ended and not yet resubscribed) never receives it;
- the WAL below is opt-in and in-process only: it is not enabled unless `MUXCORE_EVENT_JOURNAL_PATH` is set, and segments are pruned beyond 100 regardless of subscriber progress.

The publisher-supplied `Source` is not verified, and `EventService/Subscribe` is open to any mesh caller. Do not use the bus for anything that must reach every consumer or that grants authority — user erasure, for example, uses the identity provider's ledger RPCs in `proto/muxcore/auth/v1/auth.proto` (ADR-0035), not an event — and do not put identifying data in payloads.

---

## Pub/Sub

### Subscribe

```go
bus.Subscribe(ctx, "media.added", func(ctx context.Context, event contracts.Event) error {
    // handle event
    return nil
})
```

Use `"*"` to receive all events. Each subscription gets a dedicated worker goroutine with a bounded channel (capacity 256).

### Publish

```go
bus.Publish(ctx, contracts.Event{
    Type:    "media.added",
    Source:  "my-module",
    Payload: json.RawMessage(`{"title":"Inception"}`),
})
```

`Publish` returns immediately after delivering to each subscriber's channel. Handlers run asynchronously.

### Request/Reply

```go
reply, err := bus.Request(ctx, contracts.Event{
    Type:    "media.lookup",
    Payload: ...,
}, 5*time.Second)
```

Publishes an event and waits for a reply on `event.Type + ".reply"`. Times out after the given duration.

### Module-scoped subscriptions

```go
bus.SubscribeModule(ctx, "my-module-id", "media.added", handler)
// ...
bus.UnsubscribeAll(ctx, "my-module-id") // clean up on shutdown
```

`UnsubscribeAll` cancels all subscriptions for a module in one call. Used by module lifecycle management during shutdown.

---

## Backpressure

Each subscriber has a **bounded channel** (default capacity: 256 events). If a subscriber's channel is full when an event is published:

- The event is **dropped** for that subscriber (not for others)
- The subscriber's `dropped` counter increments
- A warning is logged with the drop count

The global publish deadline (60 seconds) stops dispatching to remaining subscribers if the full deadline is exceeded — this prevents a cascade of slow subscribers blocking Publish indefinitely.

### Monitoring backpressure

```go
// Total events dropped across all subscribers
dropped := bus.DroppedEvents()

// Per-subscriber stats (processed, dropped, avg latency)
stats := bus.SubscriberStats()

// Grouped by event type for the admin API
typeCounts := bus.SubscriptionStats()
```

### Handler timeout

Every handler invocation runs with a fixed 30-second deadline
(`defaultHandlerTimeout` in `internal/events/memory.go`). There is no per-module
override: `MemoryBus` has no `SetSubscriberTimeout` method. A handler that needs
longer should hand the work to its own queue and return.

---

## Write-Ahead Log (WAL)

The WAL provides event persistence and in-process catch-up replay (`SubscribeFrom`) for subscribers that register late. It does not make delivery reliable: it is off unless configured, the gRPC `Replay` RPC has no live tail, events carry no sequence over gRPC, and old segments are pruned once more than 100 exist even if a subscriber has not read them.

### Enabling

```bash
MUXCORE_EVENT_JOURNAL_PATH=/var/lib/muxcore/events
```

Or in code:

```go
bus.EnableWAL("/var/lib/muxcore/events")
defer bus.CloseWAL()
```

### Segment layout

```
/var/lib/muxcore/events/
├── events-00000000000000000001.wal   # first segment
├── events-00000000000000001025.wal   # after rotation
└── events-00000000000000002049.wal   # current
```

Each segment holds up to 32 MB. Entries are JSONL with a monotonically increasing sequence number.

### Catch-up replay

```go
// Replay all events since seq 1000, then subscribe live
bus.SubscribeFrom(ctx, "media.added", handler, 1000)

// Replay from the beginning
bus.SubscribeFrom(ctx, "media.added", handler, 0)
```

Replay is synchronous (handler called in order) then the subscription goes live for new events.

### Segment pruning

Tell the WAL when subscribers no longer need old events:

```go
bus.UpdateMinSubscriberSeq(minSeq)
```

Segments whose `LastSeq < minSeq` are deleted. Call this periodically as subscribers advance.

### WAL control variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCORE_EVENT_JOURNAL_PATH` | `""` | Enable WAL at this directory path |
| `MUXCORE_EVENT_REPLAY` | `true` | Replay WAL to late subscribers on startup |

---

## Subscription stats (admin API)

The admin endpoint `/admin/subscriptions` reports:

```json
{
  "total_subscribers": 12,
  "event_types": 5,
  "subscriptions": [
    { "event_type": "media.added", "subscriber_count": 3 },
    { "event_type": "*", "subscriber_count": 1 }
  ]
}
```

---

## Monitoring backpressure

```go
// Total dropped events across all subscribers
dropped := bus.DroppedEvents()

// Per-subscriber: processed count, dropped count, avg latency
stats := bus.SubscriberStats()
// → []SubscriberStat{EventType, ModuleID, Processed, Dropped, AvgLatencyUs}

// Active subscriptions grouped by type (for metrics)
typeCounts := bus.SubscriptionStats()

// Total active subscriptions (for health probes)
n := bus.SubscriberCount()
```

**Alert trigger:** `DroppedEvents()` growing means one or more subscribers
are processing events slower than they're published, and those events are
lost (at-most-once delivery). Make the handlers faster or move slow work off
the handler; the buffer capacity (256) and handler timeout (30 s) are
constants, not settings.

---

## WAL integrity and recovery

The WAL uses a 256KB write buffer per segment. If muxcored crashes mid-write,
the last segment may have a partial JSON line. On restart, `scanLastSeq()`
skips unparseable lines and logs a warning with the count.

**Check for WAL corruption:**

```bash
# Count skipped lines in the WAL directory
grep -r "" ~/.muxcore/events/ | wc -l
```

The WAL does not currently have a standalone verification tool. Corrupted
segments should be removed manually — events in the corrupted segment will be
replayed up to the last valid line.

---

## Implementation

| File | Purpose |
|------|---------|
| `internal/events/memory.go` | `MemoryBus` — in-memory pub/sub with backpressure |
| `internal/events/wal.go` | `WALWriter` — segmented write-ahead log |
| `pkg/contracts/events.go` | `EventBus` interface, `Event` struct |
