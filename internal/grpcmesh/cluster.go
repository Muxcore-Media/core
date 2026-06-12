package grpcmesh

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
)

// Cluster adapts DiscoveryServer to implement contracts.Cluster.
// Created by main.go and passed to meshClient.SetCluster() for cross-node
// module call routing.
type Cluster struct {
	ds     *DiscoveryServer
	events chan contracts.ClusterEvent
	once   sync.Once
}

// NewCluster creates a Cluster adapter around the given DiscoveryServer.
func NewCluster(ds *DiscoveryServer) *Cluster {
	return &Cluster{
		ds:     ds,
		events: make(chan contracts.ClusterEvent, 64),
	}
}

// Start satisfies contracts.Cluster. The discovery server is already started
// by the DiscoveryServer constructor goroutines.
func (c *Cluster) Start(_ context.Context) error {
	c.once.Do(func() {
		go c.pollEvents()
	})
	return nil
}

// Stop satisfies contracts.Cluster.
func (c *Cluster) Stop(_ context.Context) error {
	c.ds.Close()
	return nil
}

// Members satisfies contracts.Cluster.
func (c *Cluster) Members() []contracts.NodeInfo {
	pbMembers := c.ds.MembersSnapshot()
	members := make([]contracts.NodeInfo, 0, len(pbMembers))
	for _, m := range pbMembers {
		members = append(members, protoNodeToContract(m))
	}
	return members
}

// Leader satisfies contracts.Cluster.
func (c *Cluster) Leader() *contracts.NodeInfo {
	ds := c.ds
	ds.mu.RLock()
	leaderID := ds.leaderID
	pb, ok := ds.members[leaderID]
	ds.mu.RUnlock()
	if !ok || pb == nil {
		return nil
	}
	n := protoNodeToContract(pb)
	return &n
}

// LocalNode satisfies contracts.Cluster.
func (c *Cluster) LocalNode() contracts.NodeInfo {
	return protoNodeToContract(c.ds.LocalNode())
}

// Events satisfies contracts.Cluster. Returns a channel of cluster events
// detected by polling the member list. The channel is closed when the
// cluster is stopped.
func (c *Cluster) Events() <-chan contracts.ClusterEvent {
	return c.events
}

// Health satisfies contracts.Cluster.
func (c *Cluster) Health(_ context.Context) error {
	return nil
}

// FindNodesByLabel satisfies contracts.Cluster.
func (c *Cluster) FindNodesByLabel(_ context.Context, label, value string) []contracts.NodeInfo {
	members := c.ds.MembersSnapshot()
	var result []contracts.NodeInfo
	for _, m := range members {
		if m.GetLabels()[label] == value {
			result = append(result, protoNodeToContract(m))
		}
	}
	return result
}

// FindNodesByModule satisfies contracts.Cluster.
func (c *Cluster) FindNodesByModule(_ context.Context, moduleID string) []contracts.NodeInfo {
	members := c.ds.MembersSnapshot()
	var result []contracts.NodeInfo
	for _, m := range members {
		for _, mod := range m.GetModules() {
			if mod == moduleID {
				result = append(result, protoNodeToContract(m))
				break
			}
		}
	}
	return result
}

// protoNodeToContract converts a protobuf NodeInfo to a contracts.NodeInfo.
func protoNodeToContract(pb *discoveryv1.NodeInfo) contracts.NodeInfo {
	if pb == nil {
		return contracts.NodeInfo{}
	}
	return contracts.NodeInfo{
		ID:           pb.GetId(),
		GRPCAddr:     pb.GetGrpcAddr(),
		HTTPAddr:     pb.GetHttpAddr(),
		Labels:       pb.GetLabels(),
		ModuleIDs:    pb.GetModules(),
		ModuleHealth: pb.GetModuleHealth(),
	}
}

// pollEvents periodically checks for cluster membership changes and emits
// events on the events channel. Runs in a background goroutine.
func (c *Cluster) pollEvents() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cluster poll events panic recovered", "panic", r)
		}
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	var prevMembers map[string]*discoveryv1.NodeInfo

	for {
		select {
		case <-c.ds.stopCh:
			close(c.events)
			return
		case <-ticker.C:
		}

		current := make(map[string]*discoveryv1.NodeInfo)
		for _, m := range c.ds.MembersSnapshot() {
			current[m.GetId()] = m
		}

		if prevMembers == nil {
			prevMembers = current
			continue
		}

		// Detect new nodes, departed nodes, leader changes, and degradation.
		prevLeader := findLeaderID(prevMembers)
		currLeader := findLeaderID(current)

		for id, m := range current {
			if _, existed := prevMembers[id]; !existed {
				c.events <- contracts.ClusterEvent{
					Type: contracts.ClusterNodeJoined,
					Node: protoNodeToContract(m),
				}
			}
		}
		for id := range prevMembers {
			if _, exists := current[id]; !exists {
				node := protoNodeToContract(prevMembers[id])
				node.ID = id
				c.events <- contracts.ClusterEvent{
					Type: contracts.ClusterNodeLeft,
					Node: node,
				}
			}
		}
		if prevLeader != "" && currLeader != "" && prevLeader != currLeader {
			if leader, ok := current[currLeader]; ok {
				c.events <- contracts.ClusterEvent{
					Type:     contracts.ClusterLeaderChanged,
					Node:     protoNodeToContract(leader),
					LeaderID: currLeader,
				}
			}
		}

		// Detect node degradation: any node whose ModuleHealth has new
		// non-empty entries since the last poll.
		for id, cur := range current {
			prev, existed := prevMembers[id]
			if !existed {
				continue // new node, not a degradation
			}
			if hasNewDegradation(cur.GetModuleHealth(), prev.GetModuleHealth()) {
				c.events <- contracts.ClusterEvent{
					Type: contracts.ClusterNodeDegraded,
					Node: protoNodeToContract(cur),
				}
			}
		}

		prevMembers = current
	}
}

// hasNewDegradation returns true if cur has any module health entries
// that are non-empty (degraded) where prev either had no entry or had
// an empty (healthy) entry.
func hasNewDegradation(cur, prev map[string]string) bool {
	for modID, curStatus := range cur {
		if curStatus == "" {
			continue // healthy
		}
		if prevStatus, ok := prev[modID]; !ok || prevStatus == "" {
			return true
		}
	}
	return false
}

func findLeaderID(members map[string]*discoveryv1.NodeInfo) string {
	// Leader is the alphabetically lowest node ID.
	if len(members) == 0 {
		return ""
	}
	var ids []string
	for id := range members {
		ids = append(ids, id)
	}
	// Simple O(n) min, no need to sort the whole slice.
	minID := ids[0]
	for _, id := range ids[1:] {
		if id < minID {
			minID = id
		}
	}
	return minID
}
