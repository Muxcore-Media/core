# Roadmap

> Tracked in [GitHub Project: MuxCore Roadmap](https://github.com/orgs/Muxcore-Media/projects/1)

## MVP (Initial Release)

**Goal:** Prove the architecture. Compete with \*arr on simplicity while demonstrating modularity.

### Core
- [x] Module system (lifecycle, registry)
- [x] Event bus (in-memory pub/sub; NATS available as module)
- [x] Basic scheduler (extracted to `scheduler-cron` module)
- [x] REST API gateway (extracted to `api-rest` module)
- [x] Web UI shell (HTMX + Go templates + Tailwind CSS, extracted to `admin-ui` module)
- [x] ServiceRegistry + RouteRegistrar interfaces
- [x] Auto-registration via `contracts.Register()`
- [x] Default preset (`-tags default`)

### Modules
- [x] Auth: Local accounts + API tokens provider
- [x] Downloader: qBittorrent connector
- [x] Indexer: Jackett/Prowlarr connector
- [x] Media Manager: Movies (basic Radarr replacement)
- [x] Media Library: Simple scan + import
- [x] Playback: Jellyfin connector
- [x] Notification: Discord provider
- [x] Workflow Engine: request → search → download → import → notify

### Features
- [x] Request a movie
- [x] Search indexers
- [x] Download via qBittorrent
- [x] Import into library
- [x] Notify on completion

### Deployment
- [x] Docker Compose (single node)
- [x] Setup wizard
- [x] Basic configuration UI

### Marketplace
- [x] Marketplace catalog format (`catalog.json`)
- [x] Module metadata format (`muxcore.json`)
- [x] Official marketplace repo (`marketplace-catalog`)
- [x] Official vs third-party distinction (by GitHub org)
- [x] Marketplace browser in admin UI

---

## Phase 2: Distributed

**Goal:** Prove distributed architecture. Multi-node, failover, worker pools.

- [x] Multi-node clustering — extracted to `cluster-gossip` module (#21)
- [x] Distributed worker pool (#22, renamed from "Distributed transcoding pool") — `worker-pool` module
- [x] Worker failover and task redistribution (#23) — `worker-pool` module
- [x] Storage abstraction and orchestrator layer (#53)
- [x] Storage policies — hot/cold tiering (#25) — `storage-tiering` module
- [x] Audit logging (#27) — `audit-logger` module
- [x] Prometheus metrics + Grafana dashboards (#28) — `prometheus-metrics` module
- [x] DatabaseProvider contract (#61)
- [x] CacheProvider contract (#62)
- [x] Go Module SDK (#54)
- [x] Configuration management system (#55)
- [x] gRPC protobuf contract definitions (#56)
- [x] Event schema and versioning system (#57)
- [x] Module health checking and aggregation (#58)
- [x] API rate limiting (#59)
- [x] Module dependency resolution (#60)
- [x] Multi-kind module registration (#63) — one module, multiple Kinds
- [x] Capability-based service discovery (#64) — FindByCapability
- [x] Auth gateway middleware (#65) — session validation, RBAC enforcement
- [x] Module-defined metadata schema system (#75) — modules declare their media type's fields, core validates
- [x] Tag system contract (#66)
- [x] Automated backup and restore contract (#67)
- [x] Import list contract (#68) — watchlist sync from external services
- [x] Settings provider interface (#69) — UI composition for module config
- [x] Multi-agent notification contract (#70)
- [x] Quality profile and release decision contract (#71)
- [x] Custom format and release profile contract (#72)
- [x] Folder watcher and filesystem event contract (#73)
- [x] Media info and codec analysis contract (#74)

### Modules
- [x] Distributed worker pool (`worker-pool`)
- [x] Storage tiering engine (`storage-tiering`)
- [x] Audit logger (`audit-logger`)
- [x] Prometheus metrics (`prometheus-metrics`)
- [ ] Native torrent engine
- [ ] SABnzbd downloader
- [ ] Debrid downloader
- [ ] TV Manager (Sonarr replacement)
- [ ] Music Manager (Lidarr replacement)
- [ ] Subtitle provider (Bazarr replacement)
- [ ] Transcoder: FFmpeg with GPU support
- [x] PostgreSQL database provider (`database-postgres`)
- [x] Redis cache provider (`cache-redis`)
- [ ] OIDC/SSO auth provider (`auth-oidc`)
- [ ] S3/MinIO storage provider (`storage-s3`)

---

## Phase 3: Platform

**Goal:** Become a true orchestration platform. K8s-native, multi-tenant, language-agnostic modules.

- [ ] Kubernetes operator
- [ ] Helm chart
- [ ] Multi-tenant support
- [ ] Language-agnostic module SDK (Python, TypeScript, Rust)
- [x] Module marketplace / registry (runtime install) (#40)
- [ ] OpenTelemetry tracing
- [ ] gRPC module mesh with mTLS
- [ ] Module sandboxing (gVisor)

### Modules
- [ ] Book Manager (Readarr replacement)
- [ ] Manga/Comic Manager
- [ ] Audiobook Manager
- [ ] AI tagging and content classification
- [ ] Intro/outro detection
- [ ] Storage overlay: encryption, compression, dedup
- [ ] Ceph/Rook storage provider

---

## Long-Term Vision

### Killer Features (Undifferentiated in Current Market)

| Feature | Description |
|---------|-------------|
| **Distributed transcoding pool** | GPU workers anywhere on the network |
| **Intelligent orchestration** | Move workload to idle GPU, auto-balance storage, predictive pre-transcoding |
| **Unified media graph** | Single metadata system across movies, TV, books, manga, music, audiobooks, podcasts |
| **Multi-tenant** | Almost nonexistent in homelab media — could be huge |
| **Cross-media awareness** | Request an anime → discovers manga + light novel adaptations |
| **Storage abstraction** | Local, S3, Ceph, Glacier — transparent to the user |

### Market Positioning Options

| Path | Description |
|------|-------------|
| **Option A: Ultimate Homelab Media OS** | Most likely success path. The "Unraid of media management." |
| **Option B: Distributed Content Processing Platform** | Enterprise direction. Media companies, streaming services. |
| **Option C: Media Kubernetes** | Most ambitious. Generalized orchestration for all media workloads. |

### Recommended Positioning

> **"Distributed event-driven media orchestration platform"**
>
> Not "replacement for Sonarr."

That framing changes architecture decisions and makes the project far more coherent.
