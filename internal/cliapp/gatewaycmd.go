package cliapp

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayconfig"
	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// buildFailOpen returns a reverse proxy to the given app URL, or nil if empty.
// Unmatched third-party requests are proxied here (the original application);
// nil means unmatched requests get a 404.
func buildFailOpen(appURL string) (http.Handler, error) {
	if appURL == "" {
		return nil, nil
	}
	target, err := url.Parse(appURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid fail-open URL %q", appURL)
	}
	return httputil.NewSingleHostReverseProxy(target), nil
}

func newGatewayCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Run the gateway: claim API, lease registry, and reverse-tunnel server",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runGateway(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/tunlease/config.yaml", "YAML config")
	return cmd
}

func loadGatewayConfig(path string) (gatewayconfig.Config, error) {
	var c gatewayconfig.Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	c.Defaults()
	if err = c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func buildStore(c gatewayconfig.Config, ttl time.Duration, logger *slog.Logger) (registry.Store, error) {
	if c.Registry == "redis" {
		opts, err := redis.ParseURL(c.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("parse redis_url: %w", err)
		}
		return registry.NewRedis(redis.NewClient(opts), c.RedisPrefix, c.MaxClaims, c.Whitelist, ttl, logger), nil
	}
	return registry.NewMemory(c.MaxClaims, c.Whitelist, ttl, nil, logger), nil
}

func gatewayTokens(c gatewayconfig.Config) map[string]gatewayd.Token {
	tokens := map[string]gatewayd.Token{}
	for _, t := range c.Tokens {
		tokens[t.Token] = gatewayd.Token{Owner: t.Owner}
	}
	return tokens
}

func runGateway(configPath string) error {
	c, err := loadGatewayConfig(configPath)
	if err != nil {
		return err
	}
	host, err := gatewayconfig.ResolveAdvertiseHost(c.AdvertiseHost, nil, nil)
	if err != nil {
		return fmt.Errorf("resolve advertise_host: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if len(c.Tokens) == 0 {
		logger.Warn("client authentication is disabled; only suitable for a trusted network")
	}
	tokens := gatewayTokens(c)
	ttl := time.Duration(c.TTLSeconds) * time.Second
	store, err := buildStore(c, ttl, logger)
	if err != nil {
		return err
	}
	tunnel, err := gatewayd.NewTunnel(store, tokens)
	if err != nil {
		return fmt.Errorf("create tunnel server: %w", err)
	}
	failOpen, err := buildFailOpen(c.FailOpenURL)
	if err != nil {
		return err
	}
	srv := &gatewayd.Server{Store: store, Tokens: tokens, TTL: ttl, Heartbeat: time.Duration(c.HeartbeatSeconds) * time.Second, TunnelHost: host, Tunnel: tunnel, TunnelFingerprint: tunnel.Fingerprint(), OnChange: tunnel.Sync, ControlPrefix: c.ControlPrefix, FailOpen: failOpen}
	// Lease expiry is lazy; periodic sync closes sessions whose claims expired.
	go func() {
		for range time.Tick(10 * time.Second) {
			tunnel.Sync()
		}
	}()
	logger.Info("gateway listening", "addr", c.Listen, "advertise_host", host)
	return http.ListenAndServe(c.Listen, srv.Handler())
}
