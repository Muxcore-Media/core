package main

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestKillModuleNilCmd(t *testing.T) {
	w := &watchdog{
		cmd: new(atomic.Value),
	}
	w.killModule()
}

func TestKillModuleWrongType(t *testing.T) {
	w := &watchdog{
		moduleID: "test",
		cmd:      new(atomic.Value),
	}
	w.cmd.Store("not a cmd")
	w.killModule()
}

func TestCheckCoreConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	w := &watchdog{
		addrs:         []string{addr},
		checkInterval: 2 * time.Second,
	}

	if w.checkCore(context.Background()) {
		t.Error("expected false for closed port")
	}
}

func TestCheckCoreNonGRPCServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				c.Read(buf)
				c.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
			}(conn)
		}
	}()

	w := &watchdog{
		addrs:         []string{ln.Addr().String()},
		checkInterval: 4 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if w.checkCore(ctx) {
		t.Error("expected false for non-gRPC server")
	}
}

func TestCheckCoreGRPCServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				n, err := c.Read(buf)
				if err != nil || n < 24 {
					return
				}
				preface := "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
				if string(buf[:len(preface)]) != preface {
					return
				}
				c.Write([]byte{0, 0, 0, 4, 0, 0, 0, 0, 0})
			}(conn)
		}
	}()

	w := &watchdog{
		addrs:         []string{ln.Addr().String()},
		checkInterval: 4 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !w.checkCore(ctx) {
		t.Error("expected true for gRPC server")
	}
}

func TestFailoverAddressRotation(t *testing.T) {
	w := &watchdog{
		moduleID:      "test",
		modulePath:    "/nonexistent",
		addrs:         []string{"a:1", "b:2", "c:3"},
		checkInterval: time.Second,
		disconnectTO:  15 * time.Second,
		cmd:           new(atomic.Value),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w.failover(ctx)
	if w.currentAddrIdx != 1 {
		t.Errorf("expected 1, got %d", w.currentAddrIdx)
	}

	w.failover(ctx)
	if w.currentAddrIdx != 2 {
		t.Errorf("expected 2, got %d", w.currentAddrIdx)
	}

	w.failover(ctx)
	if w.currentAddrIdx != 0 {
		t.Errorf("expected 0 after wrap, got %d", w.currentAddrIdx)
	}
}

func TestFailoverIncrementsCount(t *testing.T) {
	w := &watchdog{
		moduleID:      "test",
		modulePath:    "/nonexistent",
		addrs:         []string{"a:1"},
		checkInterval: time.Second,
		disconnectTO:  15 * time.Second,
		cmd:           new(atomic.Value),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w.failover(ctx)
	if got := w.failoverCount.Load(); got != 1 {
		t.Errorf("expected failoverCount 1, got %d", got)
	}

	w.failover(ctx)
	if got := w.failoverCount.Load(); got != 2 {
		t.Errorf("expected failoverCount 2, got %d", got)
	}
}
