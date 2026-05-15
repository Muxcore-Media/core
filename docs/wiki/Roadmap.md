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

- [ ] Multi-node clustering
- [ ] Distributed transcoding pool (GPU/CPU workers)
- [ ] Worker failover and task redistribution
- [ ] S3/MinIO storage provider
- [ ] Storage policies (hot/cold tiering)
- [ ] OIDC/SSO auth
- [ ] Audit logging
- [ ] Prometheus metrics + Grafana dashboards

### Modules
- [ ] Native torrent engine
- [ ] SABnzbd downloader
- [ ] Debrid downloader
- [ ] TV Manager (Sonarr replacement)
- [ ] Music Manager (Lidarr replacement)
- [ ] Subtitle provider (Bazarr replacement)
- [ ] Transcoder: FFmpeg with GPU support

---

## Phase 3: Platform

**Goal:** Become a true orchestration platform. K8s-native, multi-tenant, language-agnostic modules.

- [ ] Kubernetes operator
- [ ] Helm chart
- [ ] Multi-tenant support
- [ ] Language-agnostic module SDK (Python, TypeScript, Rust)
- [ ] Module marketplace / registry (runtime install)
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
