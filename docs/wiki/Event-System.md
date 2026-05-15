# Event System

## Philosophy

> **Event-driven everything.** Do NOT tightly couple modules.

MuxCore uses events as the primary integration pattern. Modules publish events when state changes; other modules subscribe to react.

## Why Events?

| Benefit | Description |
|---------|-------------|
| **Loose coupling** | Publishers don't know about subscribers |
| **Extensibility** | New modules subscribe to existing events without modifying anything |
| **Resilience** | Subscriber failures don't affect publishers |
| **Observability** | Every state change is a recorded event |
| **Automation** | Workflow modules compose events into pipelines |
| **Replay** | Events can be replayed for testing or recovery |

## Event Bus (NATS)

MuxCore uses **NATS** as its event bus:

- **Pub/Sub** — Fire and forget events
- **Request/Reply** — Synchronous queries
- **Streaming (JetStream)** — Ordered, persistent event streams
- **Clustering** — Multi-node NATS for HA

## Event Structure

```go
type Event struct {
    ID          string
    Type        string    // e.g., "download.completed"
    Source      string    // e.g., "module:downloader-qbittorrent"
    Payload     []byte    // JSON or protobuf
    Metadata    map[string]string
    Timestamp   time.Time
}
```

## Well-Known Event Types

### Media Lifecycle
| Event | Publisher | Typical Subscriber |
|-------|-----------|-------------------|
| `media.requested` | UI / API | Workflow Engine |
| `media.found` | Indexer | Workflow Engine |
| `media.download.approved` | Media Manager | Downloader |
| `download.started` | Downloader | UI, Notifier |
| `download.progress` | Downloader | UI |
| `download.completed` | Downloader | Workflow Engine, Verifier |
| `download.failed` | Downloader | Workflow Engine, Notifier |
| `media.verified` | Verifier | Workflow Engine |
| `media.extracted` | Extractor | Workflow Engine |
| `media.analyzed` | Analyzer | Workflow Engine, Transcoder |
| `transcode.started` | Transcoder | UI, Notifier |
| `transcode.completed` | Transcoder | Workflow Engine, Library |
| `transcode.failed` | Transcoder | Workflow Engine, Notifier |
| `subtitle.missing` | Media Manager | Subtitle Provider |
| `subtitle.fetched` | Subtitle Provider | Media Manager |
| `library.item.added` | Media Manager | UI, Notifier, Playback |
| `library.item.removed` | Media Manager | UI, Playback |

### Playback
| Event | Publisher | Typical Subscriber |
|-------|-----------|-------------------|
| `playback.started` | Playback Module | Watch State, Analytics |
| `playback.stopped` | Playback Module | Watch State |
| `playback.progress` | Playback Module | Watch State |
| `playback.transcode.requested` | Playback Module | Transcoder |

### System
| Event | Publisher | Typical Subscriber |
|-------|-----------|-------------------|
| `module.registered` | Module | Registry, UI |
| `module.unregistered` | Module | Registry, UI |
| `module.degraded` | Module | Registry, Notifier |
| `worker.available` | Worker Agent | Scheduler |
| `worker.offline` | Worker Agent | Scheduler |
| `storage.rebalanced` | Storage Orchestrator | UI |
| `system.backup.completed` | Backup Module | UI, Notifier |

## Event-Driven Workflow Example

### Movie Request Flow
```
User clicks "Request Movie"
  → publishes media.requested

Workflow Engine receives media.requested
  → starts "movie-request" workflow

Step 1: Metadata Lookup
  → publishes metadata.lookup (request/reply to metadata provider)

Step 2: Indexer Search
  → publishes indexer.search (request/reply to all indexer modules)

Step 3: Download
  → publishes media.download.approved (consumed by preferred downloader)

Step 4: Wait for download.completed
  → downloader publishes download.completed when done

Step 5: Verification
  → publishes media.verify (request/reply to verifier)

Step 6: Import
  → publishes library.item.added

Step 7: Notify
  → publishes notification.send (consumed by notification modules)
```

Each step is an event. The workflow engine orchestrates but doesn't implement any step directly.

## Subscribing to Events

```go
// In a module's Start() method:
bus.Subscribe(ctx, "download.completed", func(ctx context.Context, event Event) error {
    var payload DownloadCompletedPayload
    json.Unmarshal(event.Payload, &payload)

    // React: start verification, send notification, etc.
    return nil
})
```

## Event Persistence & Replay

- **JetStream** stores events with configurable retention
- Events can be **replayed** for debugging or recovery
- Workflow engines can **resume** from the last completed step
- Audit logs are built from the event stream

## Anti-Patterns

### Don't: Direct Module Calls
```go
// BAD: Tight coupling
client := downloader.NewClient()
client.AddMagnet(magnet)
```

### Do: Event-Driven
```go
// GOOD: Loose coupling
bus.Publish(ctx, Event{
    Type: "media.download.approved",
    Payload: payload,
})
```
