package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayconfig"
	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

func main() {
	path := flag.String("config", "/etc/tunlease/config.yaml", "YAML config")
	flag.Parse()
	b, e := os.ReadFile(*path)
	if e != nil {
		fatal(e)
	}
	var c gatewayconfig.Config
	if e = yaml.Unmarshal(b, &c); e != nil {
		fatal(e)
	}
	c.Defaults()
	if e = c.Validate(); e != nil {
		fatal(e)
	}
	host, e := gatewayconfig.ResolveAdvertiseHost(c.AdvertiseHost, nil, nil)
	if e != nil {
		fatal(fmt.Errorf("resolve advertise_host: %w", e))
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if c.SidecarToken == "" {
		logger.Warn("/api/v1/routes is unprotected; only suitable for a trusted in-cluster network")
	}
	if len(c.Tokens) == 0 {
		logger.Warn("client authentication is disabled; only suitable for a trusted network")
	}
	tokens := map[string]gatewayd.Token{}
	for _, t := range c.Tokens {
		tokens[t.Token] = gatewayd.Token{Owner: t.Owner}
	}
	ttl := time.Duration(c.TTLSeconds) * time.Second
	var store registry.Store
	if c.Registry == "redis" {
		opts, err := redis.ParseURL(c.RedisURL)
		if err != nil {
			fatal(fmt.Errorf("parse redis_url: %w", err))
		}
		store = registry.NewRedis(redis.NewClient(opts), c.RedisPrefix, c.PortPool.Start, c.PortPool.End, c.Whitelist, ttl, logger)
	} else {
		store = registry.NewMemory(c.PortPool.Start, c.PortPool.End, c.Whitelist, ttl, nil, logger)
	}
	tunnel, e := gatewayd.NewTunnel(store, tokens)
	if e != nil {
		fatal(fmt.Errorf("create tunnel server: %w", e))
	}
	srv := &gatewayd.Server{Store: store, Tokens: tokens, SidecarToken: c.SidecarToken, TTL: ttl, Heartbeat: time.Duration(c.HeartbeatSeconds) * time.Second, TunnelHost: host, Tunnel: tunnel, TunnelFingerprint: tunnel.Fingerprint(), OnChange: tunnel.Sync}
	// Lease expiry is lazy; periodic sync closes sessions whose claims expired.
	go func() {
		for range time.Tick(10 * time.Second) {
			tunnel.Sync()
		}
	}()
	logger.Info("gateway listening", "addr", c.Listen, "advertise_host", host)
	fatal(http.ListenAndServe(c.Listen, srv.Handler()))
}
func fatal(e error) { slog.Error(e.Error()); os.Exit(1) }
