# MuxCore Go SDK

Two packages for working with MuxCore in Go:

| Package | Import | Purpose |
|---------|--------|---------|
| `client` | `github.com/Muxcore-Media/core/sdk/go/client` | Connect to a running muxcored instance |
| `mock` | `github.com/Muxcore-Media/core/sdk/go/mock` | In-memory implementations for module tests |

---

## Client — Connect to muxcored

The client wraps the five core gRPC services in an ergonomic struct.

### Installation

```go
// go.mod
require github.com/Muxcore-Media/core/sdk/go/client v0.1.0
```

### Quick start

```go
import "github.com/Muxcore-Media/core/sdk/go/client"

c, err := client.Dial("localhost:9090",
    client.WithInsecure(), // dev only — use TLS in production
)
if err != nil {
    log.Fatal(err)
}
defer c.Close()
```

### Discovery

```go
ctx := context.Background()

// Find all storage providers
providers, err := c.Discovery.FindByCapability(ctx, "storage")

// Find auth modules
authMods, err := c.Discovery.FindByRole(ctx, "auth")

// Resolve a specific module
mod, err := c.Discovery.Resolve(ctx, "downloader-qbittorrent")
if mod == nil {
    // module not registered
}

// Cluster membership
members, leaderID, err := c.Discovery.Members(ctx)
```

### Events

```go
// Publish an event
err = c.Events.Publish(ctx, "media.added", "my-tool", jsonPayload)

// Subscribe to an event stream (returns a channel)
ch, cancel, err := c.Events.Subscribe(ctx, "download.completed")
defer cancel()

for ev := range ch {
    fmt.Println("received:", ev.GetType())
}
```

### Storage

```go
// Upload a file
f, _ := os.Open("movie.mkv")
err = c.Storage.Put(ctx, "media/movie.mkv", f)

// Download a file
rc, err := c.Storage.Get(ctx, "media/movie.mkv")
defer rc.Close()
io.Copy(os.Stdout, rc)

// List objects
objects, err := c.Storage.List(ctx, "media/")

// Delete
err = c.Storage.Delete(ctx, "media/old.mkv")

// Stat
info, err := c.Storage.Stat(ctx, "media/movie.mkv")
fmt.Println("size:", info.GetSize())
```

### Health

```go
// Node-level health
resp, err := c.Health.Check(ctx, "")

// Module-specific health
resp, err := c.Health.Check(ctx, "downloader-qbittorrent")
fmt.Println("status:", resp.GetStatus())
```

### Mesh calls (inter-module RPC)

```go
// Call a module's named method directly
payload, err := c.Mesh.Call(ctx, "transcoder-ffmpeg", "transcode", requestJSON)
```

### TLS configuration

```go
import "google.golang.org/grpc/credentials"

creds, err := credentials.NewClientTLSFromFile("/etc/ssl/ca.pem", "")
c, err := client.Dial("muxcore.example.com:9090",
    client.WithGRPCOption(grpc.WithTransportCredentials(creds)),
)
```

### Join token (cluster auth)

```go
import "google.golang.org/grpc/metadata"

ctx = metadata.AppendToOutgoingContext(ctx, "x-cluster-join-token", token)
```

---

## Mock — Write module tests

The mock package provides in-memory implementations of the core interfaces
so you can test modules without running muxcored.

### Installation

```go
// go.mod
require github.com/Muxcore-Media/core/sdk/go/mock v0.1.0
```

### Usage

```go
import (
    "github.com/Muxcore-Media/core/sdk/go/mock"
    "github.com/Muxcore-Media/core/pkg/contracts"
)

func TestMyModule(t *testing.T) {
    bus  := mock.NewEventBus()
    reg  := mock.NewRegistry()
    stor := mock.NewStorage()

    mod := NewMyModule(contracts.Fabric{
        EventBus: bus,
        Registry: reg,
        Storage:  stor,
    })

    mod.Init(context.Background())

    // Assert events were published
    events := bus.PublishedEvents()
    // ...

    // Assert storage was written
    data, _ := stor.Get(context.Background(), "some/key")
    // ...
}
```

### Available mocks

| Type | Purpose |
|------|---------|
| `mock.EventBus` | In-memory pub/sub; records all published events |
| `mock.Registry` | Registers and discovers mock modules |
| `mock.Storage` | In-memory key/value storage |
| `mock.AuditLogger` | Records audit entries in memory |
| `mock.MeshClient` | Records inter-module calls |

---

## Module registration pattern

The canonical way to register a sidecar module with muxcored via gRPC is
handled by the module binary's entrypoint using the proto-generated stubs
(`muxcore.module.v1.ModuleRegistration`). The client package does not
abstract this because sidecar modules typically use the generated proto
stubs directly via the mesh address passed as `--muxcore-mesh-addr`.

See [Writing Modules](https://github.com/Muxcore-Media/core/wiki/Writing-Modules)
for the complete sidecar module pattern.
