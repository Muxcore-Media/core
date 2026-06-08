// Package client provides a Go client for connecting to a running muxcored instance.
//
// The Client wraps the five core gRPC services — Discovery, Events, Storage,
// Health, and Mesh — behind a single ergonomic struct. Modules and external
// tools use this to interact with a running core without dealing with gRPC
// boilerplate.
//
// Basic usage:
//
//	c, err := client.Dial("localhost:9090", client.WithInsecure())
//	defer c.Close()
//
//	modules, err := c.Discovery.FindByCapability(ctx, "storage")
//	rc, err := c.Storage.Get(ctx, "media/movie.mkv")
package client

import (
	"context"
	"fmt"
	"io"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is the top-level handle for a connection to muxcored.
// All service clients are pre-initialized on Dial; use them directly.
type Client struct {
	conn      *grpc.ClientConn
	Discovery *DiscoveryClient
	Events    *EventsClient
	Storage   *StorageClient
	Health    *HealthClient
	Mesh      *MeshClient
}

// Option configures a Client before dialing.
type Option func(*dialOptions)

type dialOptions struct {
	grpcOpts []grpc.DialOption
}

// WithInsecure disables TLS. Use only for local development.
func WithInsecure() Option {
	return func(o *dialOptions) {
		o.grpcOpts = append(o.grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// WithGRPCOption passes a raw gRPC dial option.
func WithGRPCOption(opt grpc.DialOption) Option {
	return func(o *dialOptions) {
		o.grpcOpts = append(o.grpcOpts, opt)
	}
}

// Dial connects to a muxcored gRPC endpoint and returns a Client.
// The caller must call Close() when done.
func Dial(addr string, opts ...Option) (*Client, error) {
	do := &dialOptions{}
	for _, o := range opts {
		o(do)
	}
	conn, err := grpc.NewClient(addr, do.grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("client: dial %s: %w", addr, err)
	}
	return &Client{
		conn:      conn,
		Discovery: &DiscoveryClient{discoveryv1.NewDiscoveryServiceClient(conn)},
		Events:    &EventsClient{eventsv1.NewEventServiceClient(conn)},
		Storage:   &StorageClient{storagev1.NewStorageServiceClient(conn)},
		Health:    &HealthClient{healthv1.NewHealthServiceClient(conn)},
		Mesh:      &MeshClient{meshv1.NewModuleMeshClient(conn)},
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// --- Discovery ---

// DiscoveryClient wraps the DiscoveryService gRPC client with ergonomic methods.
type DiscoveryClient struct {
	raw discoveryv1.DiscoveryServiceClient
}

// FindByCapability returns all modules advertising the given capability string.
func (d *DiscoveryClient) FindByCapability(ctx context.Context, capability string) ([]*discoveryv1.ModuleInfoProto, error) {
	resp, err := d.raw.FindByCapability(ctx, &discoveryv1.FindByCapabilityRequest{Capability: capability})
	if err != nil {
		return nil, err
	}
	return resp.GetModules(), nil
}

// FindByRole returns all modules with the given role string.
func (d *DiscoveryClient) FindByRole(ctx context.Context, role string) ([]*discoveryv1.ModuleInfoProto, error) {
	resp, err := d.raw.FindByRole(ctx, &discoveryv1.FindByRoleRequest{Role: role})
	if err != nil {
		return nil, err
	}
	return resp.GetModules(), nil
}

// Resolve looks up a single module by ID. Returns (nil, nil) if not found.
func (d *DiscoveryClient) Resolve(ctx context.Context, moduleID string) (*discoveryv1.ModuleInfoProto, error) {
	resp, err := d.raw.Resolve(ctx, &discoveryv1.ResolveRequest{ModuleId: moduleID})
	if err != nil {
		return nil, err
	}
	if !resp.GetFound() {
		return nil, nil
	}
	return resp.GetModule(), nil
}

// Members returns the current cluster member list.
func (d *DiscoveryClient) Members(ctx context.Context) ([]*discoveryv1.NodeInfo, string, error) {
	resp, err := d.raw.Members(ctx, &discoveryv1.MembersRequest{})
	if err != nil {
		return nil, "", err
	}
	return resp.GetMembers(), resp.GetLeaderId(), nil
}

// Join requests cluster membership. Sends joinToken via metadata if non-empty.
func (d *DiscoveryClient) Join(ctx context.Context, node *discoveryv1.NodeInfo) (*discoveryv1.JoinResponse, error) {
	return d.raw.Join(ctx, &discoveryv1.JoinRequest{Node: node})
}

// Leave gracefully removes a node from the cluster.
func (d *DiscoveryClient) Leave(ctx context.Context, nodeID string) error {
	_, err := d.raw.Leave(ctx, &discoveryv1.LeaveRequest{NodeId: nodeID})
	return err
}

// Raw returns the underlying gRPC client for advanced use.
func (d *DiscoveryClient) Raw() discoveryv1.DiscoveryServiceClient { return d.raw }

// --- Events ---

// EventsClient wraps the EventService gRPC client.
type EventsClient struct {
	raw eventsv1.EventServiceClient
}

// Publish publishes an event to the core event bus.
func (e *EventsClient) Publish(ctx context.Context, eventType, source string, payload []byte) error {
	_, err := e.raw.Publish(ctx, &eventsv1.PublishRequest{
		Event: &eventsv1.Event{
			Type:    eventType,
			Source:  source,
			Payload: payload,
		},
	})
	return err
}

// Subscribe opens a streaming subscription for the given event type.
// Returns a channel of events and a cancel func. Close cancel when done.
func (e *EventsClient) Subscribe(ctx context.Context, eventType string) (<-chan *eventsv1.Event, context.CancelFunc, error) {
	subCtx, cancel := context.WithCancel(ctx)
	stream, err := e.raw.Subscribe(subCtx, &eventsv1.SubscribeRequest{EventTypes: []string{eventType}})
	if err != nil {
		cancel()
		return nil, nil, err
	}

	ch := make(chan *eventsv1.Event, 64)
	go func() {
		defer close(ch)
		for {
			ev, err := stream.Recv()
			if err != nil {
				return
			}
			select {
			case ch <- ev:
			case <-subCtx.Done():
				return
			}
		}
	}()

	return ch, cancel, nil
}

// Raw returns the underlying gRPC client.
func (e *EventsClient) Raw() eventsv1.EventServiceClient { return e.raw }

// --- Storage ---

// StorageClient wraps the StorageService gRPC client.
type StorageClient struct {
	raw storagev1.StorageServiceClient
}

// Put uploads an object. Reads all of r before sending.
func (s *StorageClient) Put(ctx context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("storage.Put: read: %w", err)
	}

	stream, err := s.raw.Put(ctx)
	if err != nil {
		return fmt.Errorf("storage.Put: open stream: %w", err)
	}

	// Send header chunk with metadata.
	if err := stream.Send(&storagev1.PutRequest{
		Key:       key,
		Chunk:     data,
		TotalSize: int64(len(data)),
	}); err != nil {
		return fmt.Errorf("storage.Put: send: %w", err)
	}

	_, err = stream.CloseAndRecv()
	return err
}

// Get downloads an object and returns it as a ReadCloser.
// The caller must close the returned reader.
func (s *StorageClient) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	stream, err := s.raw.Get(ctx, &storagev1.GetRequest{Key: key})
	if err != nil {
		return nil, fmt.Errorf("storage.Get: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				pw.Close()
				return
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := pw.Write(chunk.GetChunk()); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()

	return pr, nil
}

// Delete removes an object.
func (s *StorageClient) Delete(ctx context.Context, key string) error {
	_, err := s.raw.Delete(ctx, &storagev1.DeleteRequest{Key: key})
	return err
}

// Stat returns metadata for an object. Returns (nil, nil) if the object does not exist.
func (s *StorageClient) Stat(ctx context.Context, key string) (*storagev1.StatResponse, error) {
	resp, err := s.raw.Stat(ctx, &storagev1.StatRequest{Key: key})
	if err != nil {
		return nil, err
	}
	if !resp.GetFound() {
		return nil, nil
	}
	return resp, nil
}

// List returns all objects under the given prefix.
func (s *StorageClient) List(ctx context.Context, prefix string) ([]*storagev1.StatResponse, error) {
	resp, err := s.raw.List(ctx, &storagev1.ListRequest{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	return resp.GetObjects(), nil
}

// Capabilities returns the capability strings advertised by the storage provider.
func (s *StorageClient) Capabilities(ctx context.Context) ([]string, error) {
	resp, err := s.raw.Capabilities(ctx, &storagev1.CapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetCapabilities(), nil
}

// Raw returns the underlying gRPC client.
func (s *StorageClient) Raw() storagev1.StorageServiceClient { return s.raw }

// --- Health ---

// HealthClient wraps the HealthService gRPC client.
type HealthClient struct {
	raw healthv1.HealthServiceClient
}

// Check returns the health status of the node or a specific module.
// Pass moduleID="" to check overall node health.
func (h *HealthClient) Check(ctx context.Context, moduleID string) (*healthv1.HealthCheckResponse, error) {
	return h.raw.Check(ctx, &healthv1.HealthCheckRequest{ModuleId: moduleID})
}

// Raw returns the underlying gRPC client.
func (h *HealthClient) Raw() healthv1.HealthServiceClient { return h.raw }

// --- Mesh ---

// MeshClient wraps the ModuleMesh gRPC client for inter-module calls.
type MeshClient struct {
	raw meshv1.ModuleMeshClient
}

// Call sends a request to a module's named method and returns the response payload.
func (m *MeshClient) Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	resp, err := m.raw.Call(ctx, &meshv1.CallRequest{
		TargetModule: targetModule,
		Method:       method,
		Payload:      payload,
	})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, fmt.Errorf("mesh call error: %s", resp.GetError())
	}
	return resp.GetPayload(), nil
}

// Raw returns the underlying gRPC client.
func (m *MeshClient) Raw() meshv1.ModuleMeshClient { return m.raw }
