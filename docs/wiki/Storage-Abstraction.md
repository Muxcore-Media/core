# Storage Abstraction

## The Problem

Every existing media stack assumes POSIX/local filesystem access. This causes massive fragility:
- `/downloads`, `/media`, `/transcodes` are hardcoded paths
- NFS/SMB mounts break silently
- Cloud storage is an afterthought
- Atomic moves and hardlinks fail across filesystem boundaries

## The MuxCore Solution

**Storage is a module.** Everything interacts through storage capabilities — never filesystem paths.

## Core Principle

> **Modules MUST NEVER touch paths.** Use `storage.Open(mediaObjectID)`, never `os.Open("/media/movie.mkv")`.

If a module touches a path, the abstraction collapses.

## Three-Layer Storage Architecture

### Layer 1: Blob Storage
Raw object storage. Handles binary data, uploads, downloads, streaming.

```go
type StorageProvider interface {
    Put(ctx, key string, data io.Reader, size int64) error
    Get(ctx, key string) (io.ReadCloser, error)
    Delete(ctx, key string) error
    Move(ctx, src, dst string) error
    Exists(ctx, key string) (bool, error)
    Stat(ctx, key string) (ObjectInfo, error)
    List(ctx, prefix string) ([]ObjectInfo, error)
}
```

### Layer 2: Media Library
Logical media organization — NOT physical paths.

```
movie://interstellar
show://breaking-bad/s01e01
music://daft-punk/random-access-memories
```

The metadata database maps logical entities to physical storage providers.

### Layer 3: Cache Layer
Distributed edge/local caching for performance.

```
SSD cache node ← hot media
RAM cache ← transcoding scratch
Temporary import workspace
```

## Capability-Based Interfaces

Not all storage supports the same operations. MuxCore uses **small capability interfaces** rather than giant monolithic ones:

```go
type Streamable interface { Stream(...) }
type Seekable interface { Seek(...) }
type Watchable interface { Watch(...) }
type AtomicMovable interface { AtomicMove(...) }
type Hardlinkable interface { Hardlink(...) }
```

### Capability Matrix

| Capability    | Local FS | SMB  | S3      | Rclone | IPFS  |
|---------------|----------|------|---------|--------|-------|
| Atomic Move   | Yes      | Maybe| No      | Maybe  | No    |
| Random Seek   | Yes      | Yes  | Partial | Partial| No    |
| Streaming     | Yes      | Yes  | Yes     | Yes    | Maybe |
| Hardlinks     | Yes      | No   | No      | No     | No    |
| Symlinks      | Yes      | No   | No      | No     | No    |
| File Watching | Yes      | Weak | No      | No     | No    |
| Transcode Temp | Excellent| OK   | Poor    | Poor   | Poor  |

Modules detect capabilities at runtime and adapt behavior accordingly.

## Storage Provider Types

### Filesystem Providers
- Local, SMB, NFS, WebDAV

### Object Storage Providers
- S3, MinIO, Backblaze B2, Cloudflare R2, Wasabi

### Distributed Storage Providers
- Ceph, GlusterFS, SeaweedFS, Longhorn

### Overlay Providers (Storage Middleware)
These are the most powerful — they wrap other providers:

```
Encryption Layer
    ↓
Compression Layer
    ↓
Replication Layer
    ↓
S3 Provider
```

- **Cache overlay** — Transparent SSD/RAM caching
- **Deduplication overlay** — Block-level dedup
- **Encryption overlay** — At-rest encryption
- **Compression overlay** — Transparent compression
- **Replication overlay** — Multi-destination writes
- **Snapshot overlay** — Point-in-time recovery

## Storage Policies

Route data based on rules:

```yaml
rules:
  - media: anime
    storage: fast-nvme

  - media: old_movies
    storage: glacier

  - media: transcoding-temp
    storage: local-ssd

  - media: "*.backup"
    storage: [local-zfs, remote-s3]
    replication: true
```

## Hybrid Storage Scenarios

### Hot/Cold Tiering
- **Hot media** → NVMe local storage
- **Warm media** → HDD array
- **Cold media** → S3 Glacier / Backblaze

### Distributed Transcoding
```
Remote S3 Media
    ↓
Worker Node Pulls Segment
    ↓
Local SSD Cache (on worker)
    ↓
GPU Transcode
    ↓
Push Result To Target Storage
```

### Multi-Region Replication
```
Primary: local ZFS
Replica: remote S3
Backup: cold archive (monthly)
```

## Important Distinctions

### Media Presence ≠ Media Availability

A movie can exist **logically** (metadata present, entry in library) but the media may be:
- Still downloading
- Archived (needs retrieval)
- Partially replicated
- Temporarily unavailable (offline node)

This distinction is essential in distributed systems.

### Terminology

| Term | Meaning |
|------|---------|
| **Asset** | Raw stored binary (a file, a blob) |
| **Media Object** | Logical media entity (movie, episode, song) |
| **Storage Provider** | Physical storage backend |
| **Storage Policy** | Placement and routing rules |
| **Capability** | Feature negotiation contract |
