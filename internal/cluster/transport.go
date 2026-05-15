package cluster

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Transport handles TCP gossip and join messages from peers.
type Transport struct {
	cluster *GossipCluster
	addr    string
	ln      net.Listener
	wg      sync.WaitGroup
}

// NewTransport creates a new transport listener.
func NewTransport(cluster *GossipCluster, addr string) *Transport {
	return &Transport{
		cluster: cluster,
		addr:    addr,
	}
}

// Listen starts accepting peer connections.
func (t *Transport) Listen() error {
	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", t.addr, err)
	}
	t.ln = ln

	slog.Info("cluster transport listening", "addr", t.addr)

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-t.cluster.stopCh:
					return
				default:
					slog.Error("transport accept", "error", err)
					continue
				}
			}
			t.wg.Add(1)
			go t.handleConn(conn)
		}
	}()
	return nil
}

// Close stops the transport.
func (t *Transport) Close() error {
	if t.ln != nil {
		t.ln.Close()
	}
	t.wg.Wait()
	return nil
}

func (t *Transport) handleConn(conn net.Conn) {
	defer t.wg.Done()
	defer conn.Close()

	var msg struct {
		Type    string               `json:"type"`
		NodeID  string               `json:"node_id"`
		Member  contracts.NodeInfo   `json:"member"`
		Members []contracts.NodeInfo `json:"members"`
	}

	if err := json.NewDecoder(conn).Decode(&msg); err != nil {
		if err != io.EOF {
			slog.Debug("transport decode", "error", err)
		}
		return
	}

	switch msg.Type {
	case "join":
		t.handleJoin(conn, msg)
	case "gossip":
		t.handleGossip(conn, msg)
	default:
		slog.Debug("unknown message type", "type", msg.Type)
	}
}

func (t *Transport) handleJoin(conn net.Conn, msg struct {
	Type    string               `json:"type"`
	NodeID  string               `json:"node_id"`
	Member  contracts.NodeInfo   `json:"member"`
	Members []contracts.NodeInfo `json:"members"`
}) {
	// Register the new node
	t.cluster.MergeMemberList(msg.NodeID, []contracts.NodeInfo{msg.Member})

	// Send back our member list + leader
	members := t.cluster.Members()
	leader := t.cluster.Leader()
	leaderID := ""
	if leader != nil {
		leaderID = leader.ID
	}

	resp := map[string]any{
		"type":    "welcome",
		"members": members,
		"leader":  leaderID,
	}
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		slog.Debug("join response encode", "error", err)
	}
}

func (t *Transport) handleGossip(conn net.Conn, msg struct {
	Type    string               `json:"type"`
	NodeID  string               `json:"node_id"`
	Member  contracts.NodeInfo   `json:"member"`
	Members []contracts.NodeInfo `json:"members"`
}) {
	t.cluster.MergeMemberList(msg.NodeID, msg.Members)
}
