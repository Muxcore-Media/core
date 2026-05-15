# Module Types

MuxCore defines **7 formal module classes**. Each class has a defined contract (Go interface + protobuf definition).

---

## 1. Provider Modules

**Provide data and services.** One-way data flow into the platform.

### Examples
- **Indexers** — Search torrent/usenet indexers (replaces Prowlarr)
- **Metadata Providers** — TMDB, TVDB, AniDB, MusicBrainz
- **Subtitle Providers** — OpenSubtitles, Subscene (replaces Bazarr)
- **Notification Providers** — Discord, Telegram, Slack, email (replaces Notifiarr)

### Contract
```go
type Indexer interface {
    Name() string
    Search(ctx, SearchQuery) ([]IndexerResult, error)
    Capabilities(ctx) ([]string, error)
}
```

---

## 2. Downloader Modules

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

## 3. Media Management Modules

**Organize and manage media collections.**

### Examples
- **Movie Manager** — Replaces Radarr
- **TV Manager** — Replaces Sonarr
- **Book Manager** — Replaces Readarr
- **Music Manager** — Replaces Lidarr
- **Manga/Comic Manager** — New capability
- **Audiobook Manager** — New capability
- **Podcast Manager** — New capability

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
**Unified media graph.** A single metadata system spans movies, TV, books, manga, music, audiobooks, and podcasts — not separate databases per media type.

---

## 4. Processing Modules

**Transform and analyze media.**

### Examples
- **Transcoding** — FFmpeg-based, GPU-accelerated (replaces Tdarr)
- **Media Analysis** — Codec detection, bitrate analysis, quality scoring
- **Thumbnail Generation** — Scene detection, sprite sheets
- **AI Tagging** — Content classification, NSFW detection, genre prediction
- **Intro/Outro Detection** — Chapter marker generation
- **Compression Optimization** — Storage-aware re-encoding

---

## 5. Playback Modules

**Serve and stream media.**

### Examples
- **Streaming Server** — HLS/DASH, transcoding proxy (replaces Jellyfin)
- **DLNA Server** — Local network casting
- **Watch State Sync** — Cross-device playback position tracking
- **Transcoding Proxy** — On-the-fly format conversion for clients

---

## 6. Workflow Modules

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

## 7. Storage Modules

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
