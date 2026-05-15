# Module Types

MuxCore defines **12 formal module kinds**. Each kind has a defined contract (Go interface + protobuf definition). Modules of any kind can be added without modifying core.

---

## 1. Authentication Modules

**Handle user authentication.** The core provides the contract — it does not authenticate users itself. Auth modules tie MuxCore into existing identity infrastructure.

### Examples
- **Local Accounts** — Built-in username/password + API tokens
- **Plex Auth** — Authenticate via Plex.tv accounts
- **OAuth/OIDC** — Google, GitHub, Microsoft, Authentik, Authelia, Keycloak
- **LDAP / Active Directory** — Enterprise directory integration
- **Jellyfin/Emby Auth** — Reuse existing media server credentials

### Contract
```go
type AuthProvider interface {
    Authenticate(ctx context.Context, credentials any) (Session, error)
    Validate(ctx context.Context, token string) (Session, error)
    Revoke(ctx context.Context, token string) error
}

type Session struct {
    UserID      string
    Username    string
    Roles       []string
    Permissions []string
    Token       string
}
```

### Key Feature
**Multiple auth providers simultaneously.** Users can sign in via Plex, LDAP, or local accounts — all active at once. Authorization (RBAC) is handled by the core based on the session the auth module returns.

---

## 2. Provider Modules

**Provide data and services.** One-way data flow into the platform.

### Examples
- **Indexers** — Search torrent/usenet indexers (replaces Prowlarr)
- **Metadata Providers** — TMDB, TVDB, AniDB, MusicBrainz
- **Subtitle Providers** — OpenSubtitles, Subscene (replaces Bazarr)
- **Notification Providers** — Discord, Telegram, Slack, email (replaces Notifiarr). Users configure **per-event-type routing** — e.g. `download.completed` → Discord, `system.health.degraded` → Email, `media.requested` → Telegram. Multiple notification modules run simultaneously, each receiving only the event types routed to it.

### Contract
```go
type Indexer interface {
    Name() string
    Search(ctx, SearchQuery) ([]IndexerResult, error)
    Capabilities(ctx) ([]string, error)
}
```

---

## 3. Downloader Modules

**Abstract download engines.** One contract, many implementations.

### Examples
- **Native torrent engine** — Built-in, no external dependency
- **qBittorrent bridge** — Interfaces with existing qBittorrent via API
- **SABnzbd bridge** — Usenet downloader integration
- **Direct download manager** — HTTP/HTTPS downloads
- **Debrid provider** — Real-Debrid, AllDebrid

### Contract
```go
type Downloader interface {
    Add(ctx, DownloadTask) (string, error)
    Remove(ctx, id string, deleteData bool) error
    Pause(ctx, id string) error
    Resume(ctx, id string) error
    Status(ctx, id string) (DownloadInfo, error)
    List(ctx) ([]DownloadInfo, error)
}
```

### Key Feature
**Multiple active downloaders simultaneously.** Route downloads to different backends based on rules (e.g., torrents to native engine, usenet to SABnzbd).

---

## 4. Media Management Modules

**Organize and manage media collections.**

Media types are **user-defined and open-ended** — not restricted to a fixed list. Each time you add a new media type, you name it (any string) and associate a module with it. You can create a `"movie"` media type and attach a movie module, then create a `"comic-book"` media type and attach a comic book module. Any string works as a media type name.

### Multiple instances of the same type

You can run **multiple instances of the same media type simultaneously**, each with different modules or configurations:

- Three `"movie"` types — `"movie-720p"`, `"movie-1080p"`, `"movie-4k"` — each backed by the same movie module but configured for different resolutions.
- The same module can be reused across types, or different modules can handle the same type.
- Since the playback module is itself modular, you could build a playback module that supports resolution switching across these duplicated types, rather than relying on on-the-fly transcoding.

The possibilities are open-ended: name any media type, wire any module, run as many copies as you need.

### Example media types (not exhaustive)
- **Movie Manager** — Replaces Radarr
- **TV Manager** — Replaces Sonarr
- **Book Manager** — Replaces Readarr
- **Music Manager** — Replaces Lidarr
- **Manga/Comic Manager** — New capability
- **Audiobook Manager** — New capability
- **Podcast Manager** — New capability
- **Any custom type** — Name it, associate a module, and it works

### Contract
```go
type MediaLibrary interface {
    Add(ctx, MediaObject) error
    Remove(ctx, id string) error
    Get(ctx, id string) (MediaObject, error)
    List(ctx, mediaType, offset, limit) ([]MediaObject, error)
    Search(ctx, query string) ([]MediaObject, error)
}
```

### Key Feature
**Unified media graph.** A single metadata system spans any media type you define — not separate databases per type.

---

## 5. Processing Modules

**Transform and analyze media.**

### Examples
- **Transcoding** — FFmpeg-based, GPU-accelerated (replaces Tdarr)
- **Media Analysis** — Codec detection, bitrate analysis, quality scoring
- **Thumbnail Generation** — Scene detection, sprite sheets
- **AI Tagging** — Content classification, NSFW detection, genre prediction
- **Intro/Outro Detection** — Chapter marker generation
- **Compression Optimization** — Storage-aware re-encoding

---

## 6. Playback Modules

**Serve and stream media.**

### Examples
- **Streaming Server** — HLS/DASH, transcoding proxy (replaces Jellyfin)
- **DLNA Server** — Local network casting
- **Watch State Sync** — Cross-device playback position tracking
- **Transcoding Proxy** — On-the-fly format conversion for clients

---

## 7. Workflow Modules

**The most powerful module class.** Defines end-to-end pipelines.

### Example: Movie Request Workflow
```
1. Request received
2. Metadata lookup (TMDB)
3. Indexer search (multiple indexers)
4. Download (preferred downloader)
5. Verification (hash check, completeness)
6. Extraction (unpack, decompress)
7. Media analysis (codec, quality)
8. Transcoding (if needed)
9. Subtitle fetch (if missing)
10. Library import
11. Notification sent
```

### Key Features
- **Retries with backoff** per step
- **Idempotency** — same request doesn't double-download
- **Compensation** — rollback on failure
- **Parallel steps** where possible

---

## 8. Storage Modules

**Abstract all filesystem and object storage.**

### Examples
- **Local FS** — Direct disk access
- **SMB/NFS** — Network shares
- **S3 / MinIO / R2 / Backblaze** — Object storage
- **Ceph / GlusterFS / SeaweedFS** — Distributed storage
- **Overlay providers** — Caching, encryption, compression, replication layers

### Key Features
- **Capability-based interfaces** — not all storage supports all operations
- **Storage policies** — route data by type, age, popularity
- **Multi-provider** — hot/cold tiering, replication
- **Object IDs, never paths** — modules don't know where data physically lives

See [Storage Abstraction](Storage-Abstraction) for the full design.

---

## 9. UI Modules

**User interfaces and dashboards.**

### Examples
- **Admin UI** — HTMX + Go templates + Tailwind CSS module dashboard
- **Media Browser** — SvelteKit-based media exploration and discovery
- **Setup Wizard** — Guided first-time configuration

### Contract
```go
// UI modules use RouteRegistrar to register HTTP handlers:
type RouteRegistrar interface {
    Handle(pattern string, handler http.Handler)
    HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}
```

---

## 10. API Modules

**API endpoints beyond core /health.**

### Examples
- **REST API** — `/api/v1/modules`, `/api/v1/modules/{id}`
- **GraphQL API** — Alternative query interface
- **gRPC Gateway** — gRPC-to-REST translation

### Contract
API modules use `RouteRegistrar` like UI modules. They typically depend on `ServiceRegistry` to serve module data.

---

## 11. Event Bus Modules

**Distributed messaging backends.** The core provides an in-memory event bus. Event bus modules add distributed alternatives.

### Examples
- **NATS** — Clustered pub/sub with JetStream persistence
- **Kafka** — High-throughput event streaming
- **RabbitMQ** — AMQP-based messaging
- **Redis Streams** — Lightweight distributed messaging

### Contract
```go
type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(ctx context.Context, eventType string, handler EventHandler) error
    Unsubscribe(ctx context.Context, eventType string, handler EventHandler) error
    Request(ctx context.Context, event Event, timeout time.Duration) (Event, error)
}
```

---

## 12. Scheduler Modules

**Task scheduling and execution.**

### Examples
- **Cron Scheduler** — In-process cron-based scheduling
- **Distributed Scheduler** — Redis-backed with exactly-once guarantees
- **Kubernetes CronJobs** — Cloud-native scheduling

### Contract
```go
type Scheduler interface {
    Schedule(ctx context.Context, task Task) (string, error)
    Cancel(ctx context.Context, taskID string) error
    Status(ctx context.Context, taskID string) (TaskStatus, error)
}
```

