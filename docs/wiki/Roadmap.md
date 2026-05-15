# Roadmap

## MVP (Initial Release)

**Goal:** Prove the architecture. Compete with \*arr on simplicity while demonstrating modularity.

### Core
- [ ] Auth system (local accounts, API tokens)
- [ ] Module system (lifecycle, registry)
- [ ] Event bus (NATS pub/sub)
- [ ] Basic scheduler
- [ ] REST API gateway
- [ ] Web UI shell (Vue 3 + Tailwind)

### Modules
- [ ] Downloader: qBittorrent connector
- [ ] Indexer: Jackett/Prowlarr connector
- [ ] Media Manager: Movies (basic Radarr replacement)
- [ ] Media Library: Simple scan + import
- [ ] Playback: Jellyfin connector
- [ ] Notification: Discord provider

### Features
- [ ] Request a movie
- [ ] Search indexers
- [ ] Download via qBittorrent
- [ ] Import into library
- [ ] Notify on completion

### Deployment
- [ ] Docker Compose (single node)
- [ ] Setup wizard
- [ ] Basic configuration UI

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
- [ ] Module marketplace / registry
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
