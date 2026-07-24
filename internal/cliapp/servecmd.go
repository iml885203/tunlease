package cliapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/iml885203/tunlease/internal/sidecar"
	"github.com/spf13/cobra"
)

func newServeCommand() *cobra.Command {
	var (
		configPath, app, listen string
		dialTimeout             time.Duration
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the gateway and the path router in one process (single-host setup)",
		Long: "serve combines the gateway (claim API, lease registry, reverse-tunnel\n" +
			"server) and the path router in a single process. It suits a single host\n" +
			"in front of one application: claimed paths tunnel to a developer, every\n" +
			"other request falls open to --app. For multiple applications, deploy the\n" +
			"gateway and sidecar separately instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), configPath, listen, app, dialTimeout)
		},
	}
	f := cmd.Flags()
	f.StringVar(&configPath, "config", "/etc/tunlease/config.yaml", "gateway YAML config")
	f.StringVar(&listen, "listen", env("TUNLEASE_SERVE_LISTEN", ":8080"), "address that receives third-party traffic and gateway API")
	f.StringVar(&app, "app", env("TUNLEASE_APP_URL", "http://127.0.0.1:8081"), "application upstream URL (fail-open target)")
	f.DurationVar(&dialTimeout, "dial-timeout", envDuration("TUNLEASE_DIAL_TIMEOUT", time.Second), "upstream dial/header timeout")
	return cmd
}

// registryRoutes builds the router's route table straight from the lease
// registry, mirroring the gateway's /api/v1/routes response without HTTP.
func registryRoutes(store registry.Store, host string) []sidecar.Route {
	if host == "" {
		host = "127.0.0.1"
	}
	if net.ParseIP(host) == nil && strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	var routes []sidecar.Route
	for _, c := range store.List() {
		addr := net.JoinHostPort(host, strconv.Itoa(c.RemotePort))
		for _, p := range c.Paths {
			routes = append(routes, sidecar.Route{PathPrefix: p, TunnelAddr: addr, ClaimID: c.ID, ExpiresAt: c.ExpiresAt})
		}
	}
	return routes
}

func runServe(parent context.Context, configPath, listen, app string, dialTimeout time.Duration) error {
	c, err := loadGatewayConfig(configPath)
	if err != nil {
		return err
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

	// The router reads routes straight from the registry; tunnel_addr points at
	// the gateway's reverse-tunnel port on this same host.
	proxy, err := sidecar.New(sidecar.Config{Listen: listen, AppURL: app, DialTimeout: dialTimeout}, logger)
	if err != nil {
		return err
	}
	refresh := func() { proxy.SetRoutes(registryRoutes(store, "127.0.0.1")) }
	refresh()

	gw := &gatewayd.Server{
		Store: store, Tokens: tokens, SidecarToken: c.SidecarToken, TTL: ttl,
		Heartbeat: time.Duration(c.HeartbeatSeconds) * time.Second, TunnelHost: "127.0.0.1",
		Tunnel: tunnel, TunnelFingerprint: tunnel.Fingerprint(),
		OnChange: func() { tunnel.Sync(); refresh() },
	}

	// Dispatch by path prefix rather than nesting ServeMuxes: the gateway's mux
	// uses method+path patterns (e.g. "GET /healthz") that do not compose when
	// mounted as a subtree. Gateway control-plane paths go to the gateway; every
	// other request falls through to the router, which tunnels claimed paths and
	// fails open to the app.
	gwHandler := gw.Handler()
	router := proxy.Handler()
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/healthz" || p == "/tunnel" || strings.HasPrefix(p, "/api/v1/") {
			gwHandler.ServeHTTP(w, r)
			return
		}
		router.ServeHTTP(w, r)
	})

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Lease expiry is lazy; periodic sync closes expired sessions and refreshes
	// the router's table so expired claims fail open.
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tunnel.Sync()
				refresh()
			}
		}
	}()
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	logger.Info("serve listening", "addr", listen, "app", app)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
