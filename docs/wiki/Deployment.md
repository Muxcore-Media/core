# Deployment Strategy

MuxCore is designed for a **phased deployment journey** — from simple single-node to globally distributed.

## Phase 1: Single Node (MVP)

**Goal:** Compete with existing \*arr simplicity.

```
docker-compose.yml
├── muxcore          (core service)
├── postgres         (database)
├── redis            (cache/queues)
├── nats             (event bus)
└── modules:
    ├── downloader-qbittorrent
    ├── indexer-jackett
    ├── media-movies
    └── notifier-discord
```

### Characteristics
- Single `docker-compose up`
- All modules on one machine
- Local filesystem storage
- Simple setup wizard
- **Just as easy as installing Sonarr**

---

## Phase 2: Clustering

**Goal:** Distributed workers, failover, shared state.

```
┌─────────────────────┐
│   MuxCore Node 1    │  (primary)
│   - Core services   │
│   - PostgreSQL      │
│   - Redis           │
│   - NATS            │
└─────────┬───────────┘
          │
    ┌─────┴─────────────┐
    │                   │
┌───▼─────────┐  ┌──────▼────────┐
│ Worker 1    │  │ Worker 2      │
│ - Transcoder│  │ - Transcoder  │
│ - GPU: RTX  │  │ - GPU: GTX    │
└─────────────┘  └───────────────┘
```

### New Capabilities
- Distributed transcoding across multiple GPUs
- Worker failover (if Worker 1 dies, tasks move to Worker 2)
- Shared task queue (Redis-backed)
- Shared metadata state (PostgreSQL)
- Module mobility (move a module to a different node)

---

## Phase 3: Kubernetes / Nomad Native

**Goal:** Full cloud-native orchestration.

```
┌───────────────────────────────────────┐
│           Kubernetes Cluster           │
│                                       │
│  ┌──────────┐  ┌──────────┐          │
│  │ MuxCore  │  │ MuxCore  │  (HA)    │
│  │ Pod 1    │  │ Pod 2    │          │
│  └──────────┘  └──────────┘          │
│                                       │
│  ┌──────────────────────────────┐     │
│  │     GPU Worker Pool          │     │
│  │  ┌──────┐ ┌──────┐ ┌──────┐ │     │
│  │  │GPU 1 │ │GPU 2 │ │GPU 3 │ │     │
│  │  └──────┘ └──────┘ └──────┘ │     │
│  └──────────────────────────────┘     │
│                                       │
│  ┌──────────────────────────────┐     │
│  │     Storage                   │     │
│  │  ┌─────────┐ ┌──────────┐    │     │
│  │  │ Rook/   │ │ MinIO    │    │     │
│  │  │ Ceph    │ │ (S3)     │    │     │
│  │  └─────────┘ └──────────┘    │     │
│  └──────────────────────────────┘     │
└───────────────────────────────────────┘
```

### New Capabilities
- Auto-scaling worker pools
- Multi-node PostgreSQL (HA)
- Redis Cluster
- NATS Cluster
- Helm chart deployment
- GitOps (Flux/Argo)
- Multi-tenant support

---

## Deployment Environments

MuxCore should work across:

| Environment | Phase 1 | Phase 2 | Phase 3 |
|-------------|---------|---------|---------|
| **Home lab** | Docker compose | Cluster | K3s |
| **Seedbox** | Single binary | Cluster | K8s |
| **Enterprise** | — | Cluster | K8s/Nomad |
| **Media provider** | — | — | Full K8s |

## Module Deployment

Each module gets its own container:

```yaml
# docker-compose.yml (Phase 1)
services:
  muxcore:
    image: muxcore-media/core:latest

  downloader-qbittorrent:
    image: muxcore-media/module-downloader-qbittorrent:latest
    environment:
      - MUXCORE_ADDR=muxcore:4222
      - MODULE_TOKEN=${QBITTORRENT_TOKEN}

  media-movies:
    image: muxcore-media/module-media-movies:latest
    environment:
      - MUXCORE_ADDR=muxcore:4222
      - MODULE_TOKEN=${MEDIA_MOVIES_TOKEN}

  transcoder-ffmpeg:
    image: muxcore-media/module-transcoder-ffmpeg:latest
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
```

## Configuration Management

- **Phase 1:** YAML config file + env vars
- **Phase 2:** etcd/Consul for distributed config
- **Phase 3:** Kubernetes ConfigMaps + Secrets

## Health & Observability

- **Phase 1:** Logging + health endpoints
- **Phase 2:** Prometheus metrics + Grafana dashboards
- **Phase 3:** OpenTelemetry tracing, centralized logging (Loki/ELK)
