# Deployment Strategy

MuxCore is designed for a **phased deployment journey** — from simple single-node to globally distributed.

## Phase 1: Single Node (MVP)

**Goal:** Compete with existing \*arr simplicity.

```
docker-compose.yml
├── muxcore          (core service, includes compiled-in modules)
├── nats             (optional — only if using eventbus-nats module)
└── modules/         (compiled into binary via -tags default)
    ├── admin-ui
    ├── api-rest
    └── scheduler-cron
```

> **Note:** Phase 1 core has no external dependencies beyond the single binary. Database and cache are not needed for single-node — the in-memory event bus and local config files are sufficient. In Phase 2, database and cache are added as modules (`database-postgres`, `cache-redis`), not as core services.

### Characteristics
- Single `docker-compose up`
- All modules compiled into one binary (default preset)
- In-memory event bus (zero external dependencies for single-node)
- Local filesystem storage
- **Just as easy as installing Sonarr**

### Build Options

```bash
# Default preset — includes admin UI, REST API, cron scheduler
go build -tags default ./cmd/muxcored

# Bare core — zero modules, just the fabric
go build ./cmd/muxcored

# Custom preset — create your own imports file with the modules you want
```

---

## Phase 2: Clustering

**Goal:** Distributed workers, failover, shared state.

```
┌─────────────────────┐
│   MuxCore Node 1    │  (primary)
│   - Core services   │
│   - NATS            │
│   - DB module       │  (database-postgres, database-sqlite, etc.)
│   - Cache module    │  (cache-redis, cache-valkey, etc.)
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
- Shared task queue (backed by cache module, e.g. cache-redis)
- Shared metadata state (backed by database module, e.g. database-postgres)
- Module mobility (move a module to a different node)

> **Note:** PostgreSQL and Redis are not built into core. They are modules implementing the `DatabaseProvider` and `CacheProvider` contracts. Users pick their database and cache by installing the appropriate module — just like they pick a downloader or indexer.

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

### Phase 2+: Distributed Module Deployment *(planned)*

Phase 1 uses compile-time modules (all modules compiled into a single binary).

Each module gets its own container:

```yaml
# docker-compose.yml (Phase 2)
services:
  muxcore:
    image: muxcore-media/core:latest

  downloader-qbittorrent:
    image: muxcore-media/module-downloader-qbittorrent:latest
    environment:
      - MUXCORE_ADDR=:8080
      - MODULE_TOKEN=${QBITTORRENT_TOKEN}

  media-movies:
    image: muxcore-media/module-media-movies:latest
    environment:
      - MUXCORE_ADDR=:8080
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
