package cliapp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/spf13/cobra"
)

func newServeCommand() *cobra.Command {
	var (
		configPath, app, listen string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the gateway with built-in path routing in one process (single-host setup)",
		Long: "serve runs the gateway on a single public HTTP listener. Third-party\n" +
			"requests whose path matches an active claim are tunnelled to the\n" +
			"developer; everything else falls open to --app (or 404s if --app is\n" +
			"unset). It suits a single host in front of one application; for many\n" +
			"applications, deploy the gateway and sidecar separately.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), configPath, listen, app)
		},
	}
	f := cmd.Flags()
	f.StringVar(&configPath, "config", "/etc/tunlease/config.yaml", "gateway YAML config")
	f.StringVar(&listen, "listen", env("TUNLEASE_SERVE_LISTEN", ":8080"), "address that receives third-party traffic and gateway API")
	f.StringVar(&app, "app", os.Getenv("TUNLEASE_APP_URL"), "fail-open target for unclaimed paths (optional; 404 when unset)")
	return cmd
}

func runServe(parent context.Context, configPath, listen, app string) error {
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

	var failOpen http.Handler
	if app != "" {
		target, perr := url.Parse(app)
		if perr != nil || target.Scheme == "" || target.Host == "" {
			return fmt.Errorf("invalid --app URL %q", app)
		}
		failOpen = httputil.NewSingleHostReverseProxy(target)
	}

	gw := &gatewayd.Server{
		Store: store, Tokens: tokens, SidecarToken: c.SidecarToken, TTL: ttl,
		Heartbeat: time.Duration(c.HeartbeatSeconds) * time.Second, TunnelHost: "127.0.0.1",
		Tunnel: tunnel, TunnelFingerprint: tunnel.Fingerprint(),
		OnChange: tunnel.Sync, FailOpen: failOpen,
	}

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Lease expiry is lazy; periodic sync closes sessions whose claims expired.
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tunnel.Sync()
			}
		}
	}()
	srv := &http.Server{Addr: listen, Handler: gw.Handler(), ReadHeaderTimeout: 5 * time.Second}
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
