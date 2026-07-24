// tunlease sidecar — 3rd-endpoint 前的極簡分流 proxy。
// 每 3s 拉路由表；被認領的 path 轉 tunnel，其餘與一切故障情況 fail-open 給 app。
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iml885203/tunlease/internal/sidecar"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sidecar stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", env("TUNLEASE_SIDECAR_LISTEN", ":8080"), "proxy listen address")
	app := flag.String("app", env("TUNLEASE_APP_URL", "http://127.0.0.1:8081"), "application upstream URL")
	routes := flag.String("routes", env("TUNLEASE_ROUTES_URL", "http://tunlease-gateway:8300/api/v1/routes"), "gateway routes URL")
	token := flag.String("token", os.Getenv("TUNLEASE_SIDECAR_TOKEN"), "routes API token")
	metrics := flag.String("metrics-listen", env("TUNLEASE_METRICS_LISTEN", ":9090"), "metrics listen address")
	poll := flag.Duration("poll-interval", envDuration("TUNLEASE_POLL_INTERVAL", 3*time.Second), "route poll interval")
	maxStale := flag.Duration("max-stale", envDuration("TUNLEASE_MAX_STALE", 60*time.Second), "maximum route table staleness")
	dialTimeout := flag.Duration("dial-timeout", envDuration("TUNLEASE_DIAL_TIMEOUT", time.Second), "upstream dial/header timeout")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	p, err := sidecar.New(sidecar.Config{Listen: *listen, AppURL: *app, RoutesURL: *routes, SidecarToken: *token, PollInterval: *poll, MaxStale: *maxStale, DialTimeout: *dialTimeout}, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go p.RunPoller(ctx)
	go func() { _ = http.ListenAndServe(*metrics, promhttp.HandlerFor(p.Registry(), promhttp.HandlerOpts{})) }()
	srv := &http.Server{Addr: *listen, Handler: p.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	logger.Info("sidecar listening", "addr", *listen, "app", *app, "routes", *routes)
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
