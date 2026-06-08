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

The event bus is **deny-by-default**: if no `PublishPolicyProvider` is configured, all `Publish` calls return an error. The built-in policy (wired at bootstrap) allows core lifecycle events and delegates domain events to capability checks.

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

### Per-module timeout override

```go
// Allow this module's handlers up to 2 minutes (e.g., for long downloads)
bus.SetSubscriberTimeout("downloader-qbittorrent", 2*time.Minute)
```

Default handler timeout: 30 seconds.

---

## Write-Ahead Log (WAL)

The WAL provides event persistence and catch-up replay for subscribers that miss events during downtime or late registration.

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
are processing events slower than they're published. Tune with
`SetSubscriberTimeout` or increase subscriber buffer capacity.

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
