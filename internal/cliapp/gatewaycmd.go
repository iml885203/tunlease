package cliapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayconfig"
	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func buildOriginProxy(originURL string) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(originURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("invalid fail-open URL %q", originURL)
	}
	return httputil.NewSingleHostReverseProxy(target), nil
}

func buildFailOpen(config gatewayconfig.Config) (http.Handler, error) {
	if config.UnclaimedStatus != 0 {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(config.UnclaimedStatus), config.UnclaimedStatus)
		}), nil
	}
	return buildOriginProxy(config.FailOpenURL)
}

func newGatewayCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Run the whole-host gateway and reverse-tunnel server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGateway(cmd.Context(), configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/tunlease/config.yaml", "YAML config")
	return cmd
}

func loadGatewayConfig(path string) (gatewayconfig.Config, error) {
	var config gatewayconfig.Config
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	config.Defaults()
	if err := config.Validate(); err != nil {
		return config, err
	}
	return config, nil
}

func gatewayTokens(config gatewayconfig.Config) map[string]gatewayd.Token {
	tokens := map[string]gatewayd.Token{}
	for _, token := range config.Tokens {
		tokens[token.Token] = gatewayd.Token{Owner: token.Owner}
	}
	return tokens
}

func runGateway(parent context.Context, configPath string) error {
	config, err := loadGatewayConfig(configPath)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if len(config.Tokens) == 0 {
		logger.Warn("client authentication is disabled; only suitable for a trusted network")
	}

	store := registry.NewMemory(registry.Options{
		MaxClaims:            config.MaxClaims,
		MaxClaimsPerOwner:    config.MaxClaimsPerOwner,
		MinClaimPathSegments: config.MinClaimPathSegments,
		MaxClaimDuration:     config.MaxClaimDuration,
		Allowed:              config.Whitelist,
	}, logger)
	tunnel := gatewayd.NewTunnel(store, gatewayTokens(config))
	tunnel.DynamicClientIdentity = config.DynamicClientIdentity
	failOpen, err := buildFailOpen(config)
	if err != nil {
		return err
	}
	gateway := &gatewayd.Server{
		Store:                 store,
		Tokens:                gatewayTokens(config),
		Tunnel:                tunnel,
		FailOpen:              failOpen,
		DisableClaimList:      config.DisableClaimList,
		DynamicClientIdentity: config.DynamicClientIdentity,
	}

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := &http.Server{
		Addr:              config.Listen,
		Handler:           gateway.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("gateway listening", "addr", config.Listen, "origin", config.FailOpenURL, "unclaimed_status", config.UnclaimedStatus)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	select {
	case err = <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return err
		}
		err = <-serveErr
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
