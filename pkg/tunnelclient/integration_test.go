package tunnelclient_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

func TestAnonymousSessionDataPathAndClose(t *testing.T) {
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	go serveOneLine(local)

	remotePort := reservePort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(remotePort, remotePort, []string{"/test/"}, time.Minute, nil, logger)
	tunnel, err := gatewayd.NewTunnel(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &gatewayd.Server{
		Store:             store,
		Tokens:            nil,
		TTL:               time.Minute,
		Heartbeat:         time.Second,
		Tunnel:            tunnel,
		TunnelFingerprint: tunnel.Fingerprint(),
		OnChange:          tunnel.Sync,
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	client, err := tunnelclient.New(tunnelclient.Config{Gateway: httpServer.URL, HTTPClient: httpServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session, err := client.Start(ctx, []string{"/test/anonymous"}, local.Addr().(*net.TCPAddr).Port)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if got := session.Claim().Owner; got != "anonymous" {
		t.Fatalf("owner = %q", got)
	}

	remote, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(session.Claim().RemotePort)), 2*time.Second)
	if err != nil {
		cancel()
		_ = session.Close()
		t.Fatal(err)
	}
	_ = remote.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = fmt.Fprintln(remote, "ping"); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(remote).ReadString('\n')
	_ = remote.Close()
	if err != nil || reply != "received: ping\n" {
		t.Fatalf("reply = %q, %v", reply, err)
	}

	cancel()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if len(store.List()) != 0 {
		t.Fatalf("claim remains after Close: %#v", store.List())
	}
}

func TestSessionReclaimsExpiredLeaseWithoutEventConsumer(t *testing.T) {
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()

	remotePort := reservePort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(remotePort, remotePort, []string{"/test/"}, 100*time.Millisecond, nil, logger)
	tunnel, err := gatewayd.NewTunnel(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &gatewayd.Server{
		Store:             store,
		TTL:               100 * time.Millisecond,
		Heartbeat:         time.Second,
		Tunnel:            tunnel,
		TunnelFingerprint: tunnel.Fingerprint(),
		OnChange:          tunnel.Sync,
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client, _ := tunnelclient.New(tunnelclient.Config{Gateway: httpServer.URL, HTTPClient: httpServer.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	session, err := client.Start(ctx, []string{"/test/reclaim"}, local.Addr().(*net.TCPAddr).Port)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	initialID := session.Claim().ID
	deadline := time.Now().Add(4 * time.Second)
	for session.Claim().ID == initialID && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if current := session.Claim().ID; current == initialID {
		cancel()
		_ = session.Close()
		t.Fatalf("claim was not renewed: %s", current)
	}
	cancel()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func serveOneLine(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err == nil {
		_, _ = fmt.Fprintf(conn, "received: %s", strings.TrimSpace(line)+"\n")
	}
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
