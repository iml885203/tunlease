package cliapp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iml885203/tunlease/internal/sidecar"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
)

func newSidecarCommand() *cobra.Command {
	var (
		listen, app, routes, token, metrics string
		poll, maxStale, dialTimeout         time.Duration
	)
	cmd := &cobra.Command{
		Use:   "sidecar",
		Short: "Run the path router beside a fixed endpoint (fails open to the app)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSidecar(cmd.Context(), sidecar.Config{
				Listen: listen, AppURL: app, RoutesURL: routes, SidecarToken: token,
				PollInterval: poll, MaxStale: maxStale, DialTimeout: dialTimeout,
			}, metrics)
		},
	}
	f := cmd.Flags()
	f.StringVar(&listen, "listen", env("TUNLEASE_SIDECAR_LISTEN", ":8080"), "proxy listen address")
	f.StringVar(&app, "app", env("TUNLEASE_APP_URL", "http://127.0.0.1:8081"), "application upstream URL")
	f.StringVar(&routes, "routes", env("TUNLEASE_ROUTES_URL", "http://tunlease-gateway:8300/api/v1/routes"), "gateway routes URL")
	f.StringVar(&token, "token", os.Getenv("TUNLEASE_SIDECAR_TOKEN"), "routes API token")
	f.StringVar(&metrics, "metrics-listen", env("TUNLEASE_METRICS_LISTEN", ":9090"), "metrics listen address")
	f.DurationVar(&poll, "poll-interval", envDuration("TUNLEASE_POLL_INTERVAL", 3*time.Second), "route poll interval")
	f.DurationVar(&maxStale, "max-stale", envDuration("TUNLEASE_MAX_STALE", 60*time.Second), "maximum route table staleness")
	f.DurationVar(&dialTimeout, "dial-timeout", envDuration("TUNLEASE_DIAL_TIMEOUT", time.Second), "upstream dial/header timeout")
	return cmd
}

func runSidecar(parent context.Context, cfg sidecar.Config, metricsAddr string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	p, err := sidecar.New(cfg, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go p.RunPoller(ctx)
	go func() {
		_ = http.ListenAndServe(metricsAddr, promhttp.HandlerFor(p.Registry(), promhttp.HandlerOpts{}))
	}()
	srv := &http.Server{Addr: cfg.Listen, Handler: p.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	logger.Info("sidecar listening", "addr", cfg.Listen, "app", cfg.AppURL, "routes", cfg.RoutesURL)
	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
