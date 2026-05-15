package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

// GossipCluster implements contracts.Cluster using a simple gossip protocol.
type GossipCluster struct {
	cfg    Config
	bus    contracts.EventBus
	mu     sync.RWMutex
	member map[string]*memberState
	nodeID string
	leader string

	eventCh chan contracts.ClusterEvent
	stopCh  chan struct{}
}

type memberState struct {
	info       contracts.NodeInfo
	lastSeen   time.Time
	status     memberStatus
	nextCursor int // gossip cursor for push-pull
}

type memberStatus int

const (
	statusAlive memberStatus = iota
	statusSuspect
	statusDead
)

// NewGossipCluster creates a new gossip-based cluster.
func NewGossipCluster(cfg Config, bus contracts.EventBus) *GossipCluster {
	if cfg.NodeID == "" {
		cfg.NodeID = uuid.New().String()
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9090"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if cfg.GossipInterval == 0 {
		cfg.GossipInterval = 2 * time.Second
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 10 * time.Second
	}
	if cfg.Labels == nil {
		cfg.Labels = make(map[string]string)
	}

	return &GossipCluster{
		cfg:     cfg,
		bus:     bus,
		member:  make(map[string]*memberState),
		nodeID:  cfg.NodeID,
		eventCh: make(chan contracts.ClusterEvent, 64),
		stopCh:  make(chan struct{}),
	}
}

// Start begins cluster membership. If seed nodes are provided, it attempts to
// join them. Otherwise it forms a new single-node cluster.
func (c *GossipCluster) Start(ctx context.Context) error {
	c.mu.Lock()
	c.member[c.nodeID] = &memberState{
		info:     c.localNodeLocked(),
		lastSeen: time.Now(),
		status:   statusAlive,
	}
	c.leader = c.nodeID
	c.mu.Unlock()

	// Try to join seed nodes
	for _, addr := range c.cfg.SeedNodes {
		if err := c.joinRemote(ctx, addr); err != nil {
			slog.Warn("failed to join seed node", "addr", addr, "error", err)
			continue
		}
		slog.Info("joined cluster via seed node", "addr", addr)
		break
	}

	// Emit our join event
	c.emitClusterEvent(contracts.ClusterNodeJoined, c.localNodeLocked())

	slog.Info("cluster started", "node_id", c.nodeID, "members", len(c.member))

	// Background gossip loop
	go c.gossipLoop(ctx)

	// Background failure detector
	go c.failureDetector(ctx)

	return nil
}

// Stop gracefully leaves the cluster.
func (c *GossipCluster) Stop(ctx context.Context) error {
	close(c.stopCh)
	c.emitClusterEvent(contracts.ClusterNodeLeft, c.LocalNode())
	return nil
}

// Members returns all current members.
func (c *GossipCluster) Members() []contracts.NodeInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]contracts.NodeInfo, 0, len(c.member))
	for _, m := range c.member {
		if m.status != statusDead {
			out = append(out, m.info)
		}
	}
	return out
}

// Leader returns the current leader.
func (c *GossipCluster) Leader() *contracts.NodeInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.leader == "" {
		return nil
	}
	if m, ok := c.member[c.leader]; ok {
		info := m.info
		return &info
	}
	return nil
}

// LocalNode returns this node's info.
func (c *GossipCluster) LocalNode() contracts.NodeInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.localNodeLocked()
}

// Events returns the membership change channel.
func (c *GossipCluster) Events() <-chan contracts.ClusterEvent {
	return c.eventCh
}

// Health reports whether the cluster is operational.
func (c *GossipCluster) Health(ctx context.Context) error {
	c.mu.RLock()
	alive := 0
	for _, m := range c.member {
		if m.status == statusAlive {
			alive++
		}
	}
	c.mu.RUnlock()

	if alive == 0 {
		return fmt.Errorf("no alive members")
	}
	return nil
}

// MergeMemberList accepts a member list from a peer and merges it with ours.
// This is the core push-pull gossip mechanism.
func (c *GossipCluster) MergeMemberList(peerID string, peerMembers []contracts.NodeInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for _, info := range peerMembers {
		if info.ID == c.nodeID {
			continue
		}
		existing, ok := c.member[info.ID]
		if !ok {
			entry := &memberState{
				info:     info,
				lastSeen: now,
				status:   statusAlive,
			}
			c.member[info.ID] = entry
			slog.Info("discovered new node", "node_id", info.ID, "addr", info.GRPCAddr)
			go c.emitClusterEvent(contracts.ClusterNodeJoined, info)
			continue
		}
		if existing.info.GRPCAddr != info.GRPCAddr || existing.info.HTTPAddr != info.HTTPAddr {
			existing.info = info
		}
		existing.lastSeen = now
		if existing.status == statusDead {
			existing.status = statusAlive
			go c.emitClusterEvent(contracts.ClusterNodeJoined, info)
		}
	}
	c.electLeaderLocked()
}

func (c *GossipCluster) localNodeLocked() contracts.NodeInfo {
	return contracts.NodeInfo{
		ID:       c.nodeID,
		GRPCAddr: c.cfg.GRPCAddr,
		HTTPAddr: c.cfg.HTTPAddr,
		Labels:   c.cfg.Labels,
	}
}

func (c *GossipCluster) electLeaderLocked() {
	var lowest string
	for id, m := range c.member {
		if m.status != statusAlive {
			continue
		}
		if lowest == "" || id < lowest {
			lowest = id
		}
	}
	if lowest != "" && lowest != c.leader {
		prev := c.leader
		c.leader = lowest
		slog.Info("leader changed", "previous", prev, "new", c.leader)
		go c.emitLeaderChanged(prev, lowest)
	}
}

func (c *GossipCluster) emitClusterEvent(typ contracts.ClusterEventType, node contracts.NodeInfo) {
	c.mu.RLock()
	leader := c.leader
	c.mu.RUnlock()

	ev := contracts.ClusterEvent{
		Type:     typ,
		Node:     node,
		LeaderID: leader,
	}
	select {
	case c.eventCh <- ev:
	default:
	}
	// Also publish to event bus
	c.publishBusEvent(typ, node)
}

func (c *GossipCluster) emitLeaderChanged(prev, new string) {
	ev := contracts.ClusterEvent{
		Type:     contracts.ClusterLeaderChanged,
		LeaderID: new,
	}
	select {
	case c.eventCh <- ev:
	default:
	}
	payload, _ := json.Marshal(contracts.LeaderChangedPayload{
		PreviousLeader: prev,
		NewLeader:      new,
	})
	c.bus.Publish(context.Background(), contracts.Event{
		ID:        uuid.New().String(),
		Type:      contracts.EventClusterLeaderChanged,
		Source:    "cluster-" + c.nodeID,
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

func (c *GossipCluster) publishBusEvent(typ contracts.ClusterEventType, node contracts.NodeInfo) {
	var eventType string
	var payload any

	switch typ {
	case contracts.ClusterNodeJoined:
		eventType = contracts.EventClusterNodeJoined
		payload = contracts.NodeJoinedPayload{
			NodeID:   node.ID,
			GRPCAddr: node.GRPCAddr,
			HTTPAddr: node.HTTPAddr,
		}
	case contracts.ClusterNodeLeft:
		eventType = contracts.EventClusterNodeLeft
		payload = contracts.NodeLeftPayload{NodeID: node.ID}
	default:
		return
	}

	data, _ := json.Marshal(payload)
	c.bus.Publish(context.Background(), contracts.Event{
		ID:        uuid.New().String(),
		Type:      eventType,
		Source:    "cluster-" + c.nodeID,
		Payload:   data,
		Timestamp: time.Now(),
	})
}

func (c *GossipCluster) gossipLoop(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.GossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			peer := c.pickRandomPeer()
			if peer == "" {
				continue
			}
			if err := c.gossipWithPeer(ctx, peer); err != nil {
				slog.Debug("gossip failed", "peer", peer, "error", err)
			}
		}
	}
}

func (c *GossipCluster) pickRandomPeer() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var peers []string
	for id, m := range c.member {
		if id != c.nodeID && m.status == statusAlive {
			peers = append(peers, id)
		}
	}
	if len(peers) == 0 {
		return ""
	}
	return peers[rand.Intn(len(peers))]
}

func (c *GossipCluster) gossipWithPeer(ctx context.Context, peerID string) error {
	c.mu.RLock()
	peer, ok := c.member[peerID]
	if !ok {
		c.mu.RUnlock()
		return fmt.Errorf("peer %q not found", peerID)
	}
	addr := peer.info.GRPCAddr
	c.mu.RUnlock()

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Send our member list
	c.mu.RLock()
	members := c.Members()
	c.mu.RUnlock()

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(map[string]any{
		"type":    "gossip",
		"node_id": c.nodeID,
		"members": members,
	}); err != nil {
		return fmt.Errorf("encode gossip: %w", err)
	}

	return nil
}

func (c *GossipCluster) joinRemote(ctx context.Context, addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(map[string]any{
		"type":    "join",
		"node_id": c.nodeID,
		"member":  c.LocalNode(),
	}); err != nil {
		return fmt.Errorf("encode join: %w", err)
	}

	var resp struct {
		Type    string               `json:"type"`
		Members []contracts.NodeInfo `json:"members"`
		Leader  string               `json:"leader"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("decode join response: %w", err)
	}

	if resp.Type == "welcome" {
		c.MergeMemberList(addr, resp.Members)
	}
	return nil
}

func (c *GossipCluster) failureDetector(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.HeartbeatTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.checkFailures()
		}
	}
}

func (c *GossipCluster) checkFailures() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	leaderChanged := false
	for id, m := range c.member {
		if id == c.nodeID {
			continue
		}
		switch m.status {
		case statusAlive:
			if now.Sub(m.lastSeen) > c.cfg.HeartbeatTimeout {
				m.status = statusSuspect
				slog.Warn("node suspected dead", "node_id", id)
				go c.emitClusterEvent(contracts.ClusterNodeDegraded, m.info)
			}
		case statusSuspect:
			if now.Sub(m.lastSeen) > c.cfg.HeartbeatTimeout*2 {
				m.status = statusDead
				slog.Warn("node declared dead", "node_id", id)
				go c.emitClusterEvent(contracts.ClusterNodeLeft, m.info)
				if c.leader == id {
					c.leader = ""
					leaderChanged = true
				}
			}
		}
	}
	if leaderChanged {
		c.electLeaderLocked()
	}
}
