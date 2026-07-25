package cliapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type config struct {
	Gateway       string `yaml:"gateway"`
	Token         string `yaml:"token"`
	Insecure      bool   `yaml:"insecure"`
	DefaultScheme string `yaml:"default_scheme"`
}

func NormalizePath(p string) (string, error) {
	return tunnelclient.NormalizePath(p)
}

func NewCommandWithVersion(version, buildTime string) *cobra.Command {
	var gateway, token string
	var insecure bool
	var output string
	root := &cobra.Command{
		Use:           "tul",
		Short:         "Forward a callback path to your local service",
		Example:       "  tul claim '/webhooks/provider' -p 8080 -g callbacks.example.com",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       fmt.Sprintf("%s (%s)", version, buildTime),
	}
	root.AddGroup(
		&cobra.Group{ID: "developer", Title: "Developer commands:"},
		&cobra.Group{ID: "operator", Title: "Platform operator commands:"},
	)
	// Client flags live on the client subcommands (claim/list/release), not on
	// root, so the gateway subcommand doesn't inherit irrelevant client flags.
	addClientFlags := func(c *cobra.Command) {
		c.Flags().StringVarP(&gateway, "gateway", "g", "", "gateway base URL (env TUNLEASE_GATEWAY)")
		c.Flags().StringVarP(&token, "token", "t", "", "API token (env TUNLEASE_TOKEN)")
		c.Flags().BoolVarP(&insecure, "insecure", "k", false, "skip gateway TLS verification, e.g. a self-signed gateway (env TUNLEASE_INSECURE)")
		c.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	}
	get := func() (*tunnelclient.Client, error) {
		c, err := loadConfig()
		if err != nil {
			return nil, err
		}
		if gateway != "" {
			c.Gateway = gateway
		} else if v := os.Getenv("TUNLEASE_GATEWAY"); v != "" {
			c.Gateway = v
		}
		if token != "" {
			c.Token = token
		} else if v := os.Getenv("TUNLEASE_TOKEN"); v != "" {
			c.Token = v
		}
		if c.Gateway == "" {
			return nil, errors.New("gateway required; retry with --gateway HOST or set TUNLEASE_GATEWAY")
		}
		insec := insecure || c.Insecure || os.Getenv("TUNLEASE_INSECURE") != ""
		scheme := c.DefaultScheme
		if v := os.Getenv("TUNLEASE_DEFAULT_SCHEME"); v != "" {
			scheme = v
		}
		clientConfig := tunnelclient.Config{Gateway: c.Gateway, Token: c.Token, Insecure: insec, DefaultScheme: scheme}
		client, err := tunnelclient.New(clientConfig)
		if err != nil || c.Token != "" {
			return client, err
		}
		identity, err := clientIdentityFor(client.Gateway())
		if err != nil {
			return nil, err
		}
		clientConfig.Token = identity
		return tunnelclient.New(clientConfig)
	}

	var to int
	var detach, daemon bool
	claimCmd := &cobra.Command{
		Use:     "claim PATH [PATH...] --to PORT",
		Short:   "Temporarily forward callback path(s) to localhost",
		GroupID: "developer",
		Example: `  # Forward one exact path
  tul claim '/webhooks/provider' -p 8080 -g callbacks.example.com

  # Use /* for one child segment, or /** for every descendant
  tul claim '/webhooks/*' -p 8080`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(output); err != nil {
				return err
			}
			ui := newConsoleOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), output)
			if to < 1 || to > 65535 {
				return errors.New("local port must be between 1 and 65535")
			}
			paths := make([]string, 0, len(args))
			for _, p := range args {
				n, e := NormalizePath(p)
				if e != nil {
					return fmt.Errorf("%q: %w", p, e)
				}
				paths = append(paths, n)
			}
			c, e := get()
			if e != nil {
				return e
			}
			if e := checkLocalTarget(to); e != nil {
				if ui.json {
					ui.emitJSON(ui.err, map[string]any{
						"type": "warning", "code": "local_unavailable",
						"message": fmt.Sprintf("localhost:%d is not accepting connections; start the service to receive requests", to),
					})
				} else {
					ui.warning("WARNING: localhost:%d is not accepting connections; start the service to receive requests", to)
				}
			}
			if detach {
				// Non-blocking: spawn a background daemon and return once the
				// tunnel is up. Pass the connection options through explicitly.
				scheme := os.Getenv("TUNLEASE_DEFAULT_SCHEME")
				if scheme == "" {
					c, e := loadConfig()
					if e != nil {
						return e
					}
					scheme = c.DefaultScheme
				}
				return runDetach(ui, paths, to, gateway, c.Gateway(), token, insecure || os.Getenv("TUNLEASE_INSECURE") != "", scheme)
			}
			return runClaim(ui, c, paths, to, daemon)
		},
	}
	claimCmd.Flags().IntVarP(&to, "to", "p", 0, "local port to receive the traffic")
	claimCmd.Flags().BoolVarP(&detach, "detach", "d", false, "run in the background and return immediately (stop with tul release)")
	claimCmd.Flags().BoolVar(&daemon, "_daemon", false, "")
	_ = claimCmd.Flags().MarkHidden("_daemon")
	_ = claimCmd.MarkFlagRequired("to")
	addClientFlags(claimCmd)
	root.AddCommand(claimCmd)

	var all bool
	listCmd := &cobra.Command{
		Use:     "list",
		Short:   "Show callback paths being forwarded",
		GroupID: "developer",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(output); err != nil {
				return err
			}
			c, e := get()
			if e != nil {
				return e
			}
			return runList(newConsoleOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), output), cmd.Context(), c, all)
		},
	}
	listCmd.Flags().BoolVarP(&all, "all", "a", false, "show all owners' claims")
	addClientFlags(listCmd)
	root.AddCommand(listCmd)

	var relTo int
	releaseCmd := &cobra.Command{
		Use:     "release [PATH]",
		Short:   "Stop forwarding a claimed path",
		GroupID: "developer",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(output); err != nil {
				return err
			}
			c, e := get()
			if e != nil {
				return e
			}
			if relTo > 0 && len(args) > 0 {
				return errors.New("specify either PATH or --to, not both")
			}
			if relTo == 0 && len(args) == 0 {
				return errors.New("specify a PATH or --to PORT")
			}
			return runRelease(newConsoleOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), output), cmd.Context(), c, args, relTo)
		},
	}
	releaseCmd.Flags().IntVarP(&relTo, "to", "p", 0, "release all claims tunnelled to this local port")
	addClientFlags(releaseCmd)
	root.AddCommand(releaseCmd)

	gatewayCmd := newGatewayCommand()
	gatewayCmd.GroupID = "operator"
	root.AddCommand(gatewayCmd)
	return root
}

func validateOutput(output string) error {
	if output != "text" && output != "json" {
		return fmt.Errorf("output must be text or json, got %q", output)
	}
	return nil
}

func runClaim(ui *console, c *tunnelclient.Client, paths []string, to int, _ bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	session, e := c.Start(ctx, paths, to)
	if e != nil {
		return e
	}
	cl := session.Claim()
	// Record the holder for both foreground and detached sessions. Release uses
	// this only as a conservative liveness check; it never signals the PID.
	pid := os.Getpid()
	st := loadState()
	st.add(stateClaim{ClaimID: cl.ID, Gateway: c.Gateway(), Paths: cl.Paths, To: to, ExpiresAt: cl.ExpiresAt, PID: pid})
	saveState(st)
	defer cleanupSessionState(c.Gateway(), to, cl.Paths)

	if ui.json {
		ui.event(connectionEvent("connected", cl.Paths, to, cl.ExpiresAt))
	} else {
		ui.success("%s", connectionMessage("Connected", cl.Paths, to, cl.ExpiresAt))
		pathNoun := "path"
		if len(cl.Paths) > 1 {
			pathNoun = "paths"
		}
		ui.noticeOut("Requests will appear below. Press Ctrl+C to stop forwarding and release the %s.", pathNoun)
	}
	for {
		select {
		case <-ctx.Done():
			_ = session.Close()
			printStoppedTerminal(ui, cl.Paths)
			return nil
		case event, ok := <-session.Events():
			if !ok {
				if err := session.Err(); err != nil {
					return finishTerminalSession(ui, err, cl)
				}
				if ctx.Err() != nil {
					printStoppedTerminal(ui, cl.Paths)
				}
				return nil
			}
			switch event.Type {
			case tunnelclient.EventTunnelDisconnected:
				if ui.json {
					ui.event(map[string]any{"type": "disconnected", "state": "retrying"})
				} else {
					ui.warningOut("Connection lost; retrying…")
				}
			case tunnelclient.EventTunnelReconnected:
				previous := cl
				cl = event.Claim
				st := loadState()
				st.removeSession(c.Gateway(), to, previous.Paths)
				st.add(stateClaim{ClaimID: cl.ID, Gateway: c.Gateway(), Paths: cl.Paths, To: to, ExpiresAt: cl.ExpiresAt, PID: pid})
				saveState(st)
				if ui.json {
					ui.event(connectionEvent("reconnected", cl.Paths, to, cl.ExpiresAt))
				} else {
					ui.status("%s", connectionMessage("Reconnected", cl.Paths, to, cl.ExpiresAt))
				}
			case tunnelclient.EventLocalTargetError:
				if ui.json {
					ui.event(map[string]any{
						"type": "local_error", "target": fmt.Sprintf("localhost:%d", to),
						"local_port": to, "code": "local_unavailable", "message": localTargetError(event.Err),
					})
				} else {
					ui.warningOut("Could not reach localhost:%d: %s", to, localTargetError(event.Err))
				}
			case tunnelclient.EventRequestActivity:
				if ui.json {
					ui.event(map[string]any{
						"type": "request", "method": event.Method, "path": event.Path,
						"status": event.Status, "duration_ms": event.Duration.Milliseconds(),
					})
				} else {
					ui.activity(event.Method, event.Path, event.Status, formatActivityDuration(event.Duration))
				}
			}
		}
	}
}

func printStoppedTerminal(ui *console, paths []string) {
	if ui.json {
		ui.event(map[string]any{"type": "released", "paths": paths})
	} else {
		ui.info("\nreleased, tunnel closed")
	}
}

func finishTerminalSession(ui *console, err error, claim tunnelclient.Claim) error {
	terminal, expected := expectedTerminalReason(err)
	if !expected {
		return err
	}
	printExpectedTerminal(ui, terminal, claim)
	return nil
}

type expectedTerminal string

const (
	terminalExpired  expectedTerminal = "expired"
	terminalReleased expectedTerminal = "released"
)

func expectedTerminalReason(err error) (expectedTerminal, bool) {
	var apiErr *tunnelclient.APIError
	if !errors.As(err, &apiErr) {
		return "", false
	}
	switch apiErr.Code {
	case "claim_expired":
		return terminalExpired, true
	case "claim_released":
		return terminalReleased, true
	default:
		return "", false
	}
}

func printExpectedTerminal(ui *console, terminal expectedTerminal, claim tunnelclient.Claim) {
	switch terminal {
	case terminalExpired:
		if ui.json {
			event := map[string]any{"type": "expired", "paths": claim.Paths}
			if claim.ExpiresAt != nil {
				event["expired_at"] = claim.ExpiresAt.Format(time.RFC3339Nano)
			}
			ui.event(event)
			return
		}
		if claim.ExpiresAt != nil {
			ui.info("Claim expired at %s; tunnel closed.", claim.ExpiresAt.Local().Format("15:04:05"))
		} else {
			ui.info("Claim expired; tunnel closed.")
		}
	case terminalReleased:
		if ui.json {
			ui.event(map[string]any{"type": "released", "paths": claim.Paths})
		} else {
			ui.info("Claim released; tunnel closed.")
		}
	}
}

func connectionEvent(eventType string, paths []string, to int, expiresAt *time.Time) map[string]any {
	event := map[string]any{
		"type": eventType, "paths": paths, "target": fmt.Sprintf("localhost:%d", to), "local_port": to,
	}
	if expiresAt != nil {
		event["expires_at"] = expiresAt.Format(time.RFC3339Nano)
	}
	return event
}

func connectionMessage(status string, paths []string, to int, expiresAt *time.Time) string {
	if expiresAt != nil {
		return fmt.Sprintf("%s until %s: %s → localhost:%d", status, expiresAt.Local().Format("15:04:05"), strings.Join(paths, " "), to)
	}
	return fmt.Sprintf("%s: %s → localhost:%d", status, strings.Join(paths, " "), to)
}

func localTargetError(err error) string {
	if err == nil {
		return "unknown error"
	}
	var syscallErr *os.SyscallError
	if errors.As(err, &syscallErr) {
		return syscallErr.Err.Error()
	}
	return err.Error()
}

func formatActivityDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1ms"
	}
	return duration.Round(time.Millisecond).String()
}

func checkLocalTarget(port int) error {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), 250*time.Millisecond)
	if err != nil {
		return err
	}
	return connection.Close()
}

func cleanupSessionState(gateway string, to int, paths []string) {
	st := loadState()
	st.removeSession(gateway, to, paths)
	saveState(st)
}

func runList(ui *console, ctx context.Context, c *tunnelclient.Client, all bool) error {
	st := loadState()
	mine := map[string]stateClaim{}
	for _, s := range st.Claims {
		mine[s.ClaimID] = s
	}
	claims, e := c.List(ctx)
	if e != nil {
		if claimListUnavailable(e) {
			return &claimListUnavailableError{}
		}
		return e
	}
	shown := 0
	jsonClaims := make([]map[string]any, 0)
	for _, x := range claims {
		s, own := mine[x.ID]
		if !all && !own {
			continue
		}
		target := "owner=" + x.Owner
		suffix := ""
		status := "connected"
		expiring := false
		if x.ExpiresAt != nil {
			remaining := time.Until(*x.ExpiresAt).Round(time.Second)
			if remaining < 0 {
				remaining = 0
			}
			status = "expires in " + remaining.String()
			expiring = true
		}
		if own {
			target = fmt.Sprintf("→ localhost:%d", s.To)
			suffix = "  (you)"
		}
		if ui.json {
			item := map[string]any{
				"paths": x.Paths, "owner": x.Owner, "started_at": x.StartedAt.Format(time.RFC3339Nano),
				"mine": own, "status": "connected",
			}
			if own {
				item["target"] = fmt.Sprintf("localhost:%d", s.To)
				item["local_port"] = s.To
			}
			if x.ExpiresAt != nil {
				item["expires_at"] = x.ExpiresAt.Format(time.RFC3339Nano)
			}
			jsonClaims = append(jsonClaims, item)
			shown++
			continue
		}
		if shown == 0 {
			ui.claimHeader()
		}
		ui.claimRow(strings.Join(x.Paths, ","), target, status, x.StartedAt.Local().Format("15:04:05"), suffix, own, expiring)
		shown++
	}
	if ui.json {
		ui.event(map[string]any{"type": "claim_list", "claims": jsonClaims})
		return nil
	}
	if shown == 0 {
		if all {
			ui.info("no active claims")
		} else {
			ui.info("no active claims of yours (use --all to see everyone's)")
		}
	}
	return nil
}

func runRelease(ui *console, ctx context.Context, c *tunnelclient.Client, args []string, relTo int) error {
	st := loadState()
	if relTo > 0 {
		released := 0
		alreadyAbsent := 0
		failures := make([]releaseFailure, 0)
		// Iterate a snapshot because successful releases remove entries from st.
		for _, s := range append([]stateClaim(nil), st.Claims...) {
			if s.To == relTo && s.Gateway == c.Gateway() {
				if e := c.Release(ctx, s.ClaimID); e != nil {
					if claimAlreadyAbsent(e) {
						if s.PID > 0 && processAlive(s.PID) {
							failures = append(failures, releaseFailure{
								Paths: s.Paths, Code: "release_pending",
								Message: "tunnel process is still running while its claim reconnects",
							})
							continue
						}
						st.removeByID(s.ClaimID)
						saveState(st)
						printReleased(ui, s.Paths, relTo, true)
						alreadyAbsent++
						continue
					}
					code, message, _ := errorDetails(e)
					failures = append(failures, releaseFailure{Paths: s.Paths, Code: code, Message: message})
					continue
				}
				st.removeByID(s.ClaimID)
				// Persist each completed release so a later failure cannot
				// resurrect stale local state.
				saveState(st)
				printReleased(ui, s.Paths, relTo, false)
				released++
			}
		}
		if len(failures) > 0 {
			return &partialReleaseError{
				Released: released, AlreadyAbsent: alreadyAbsent, Failures: failures,
				LocalPort: relTo, Gateway: c.Gateway(),
			}
		}
		if ui.json {
			ui.event(map[string]any{
				"type": "release_summary", "released": released, "failed": 0,
				"already_absent": alreadyAbsent, "local_port": relTo, "gateway": c.Gateway(),
			})
			return nil
		}
		if released == 0 && alreadyAbsent == 0 {
			ui.info("no claims recorded for local port %d on this gateway", relTo)
		}
		return nil
	}
	target, e := NormalizePath(args[0])
	if e != nil {
		return e
	}
	// 先查本機 state，找不到再翻遠端（可能是別台機器留下的租約）
	for _, s := range st.Claims {
		if s.Gateway == c.Gateway() && contains(s.Paths, target) {
			if e := c.Release(ctx, s.ClaimID); e != nil {
				if claimAlreadyAbsent(e) {
					if s.PID > 0 && processAlive(s.PID) {
						return &backgroundReleasePendingError{}
					}
					st.removeByID(s.ClaimID)
					saveState(st)
					printReleased(ui, []string{target}, s.To, true)
					return nil
				}
				return e
			}
			st.removeByID(s.ClaimID)
			saveState(st)
			printReleased(ui, []string{target}, s.To, false)
			return nil
		}
	}
	claims, e := c.List(ctx)
	if e != nil {
		if claimListUnavailable(e) {
			return &claimListUnavailableError{}
		}
		return e
	}
	for _, x := range claims {
		if contains(x.Paths, target) {
			if e := c.Release(ctx, x.ID); e != nil {
				if claimAlreadyAbsent(e) {
					printMissingRelease(ui, target, c.Gateway())
					return nil
				}
				return e
			}
			if ui.json {
				ui.event(map[string]any{"type": "released", "paths": []string{target}})
			} else {
				ui.success("released %s", target)
			}
			return nil
		}
	}
	printMissingRelease(ui, target, c.Gateway())
	return nil
}

func printMissingRelease(ui *console, target, gateway string) {
	if ui.json {
		ui.event(map[string]any{
			"type": "release_summary", "released": 0, "failed": 0,
			"paths": []string{target}, "gateway": gateway,
		})
		return
	}
	ui.info("no active claim found for %s", target)
}

func claimAlreadyAbsent(err error) bool {
	var apiErr *tunnelclient.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "claim_not_found"
}

func claimListUnavailable(err error) bool {
	var apiErr *tunnelclient.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Code == ""
}

type claimListUnavailableError struct{}

func (e *claimListUnavailableError) Error() string {
	return "gateway does not expose claim lookup"
}

func (e *claimListUnavailableError) CLIErrorCode() string {
	return "claim_list_unavailable"
}

type backgroundReleasePendingError struct{}

func (e *backgroundReleasePendingError) Error() string {
	return "tunnel process is reconnecting; its release could not yet be confirmed"
}

func (e *backgroundReleasePendingError) CLIErrorCode() string {
	return "release_pending"
}

func printReleased(ui *console, paths []string, localPort int, alreadyAbsent bool) {
	if ui.json {
		event := map[string]any{"type": "released", "paths": paths, "local_port": localPort}
		if alreadyAbsent {
			event["already_absent"] = true
		}
		ui.event(event)
		return
	}
	if alreadyAbsent {
		ui.info("already released %s", strings.Join(paths, " "))
		return
	}
	ui.success("released %s", strings.Join(paths, " "))
}

type partialReleaseError struct {
	Released      int
	AlreadyAbsent int
	Failures      []releaseFailure
	LocalPort     int
	Gateway       string
}

type releaseFailure struct {
	Paths   []string `json:"paths"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
}

func (e *partialReleaseError) Error() string {
	failures := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		failures = append(failures, fmt.Sprintf("%s: %s", strings.Join(failure.Paths, " "), failure.Message))
	}
	return fmt.Sprintf(
		"partial release: released %d, already absent %d, failed %d (%s)",
		e.Released,
		e.AlreadyAbsent,
		len(e.Failures),
		strings.Join(failures, "; "),
	)
}

func (e *partialReleaseError) CLIErrorCode() string {
	return "partial_release"
}

func (e *partialReleaseError) CLIErrorFields() map[string]any {
	return map[string]any{
		"released":       e.Released,
		"already_absent": e.AlreadyAbsent,
		"failed":         len(e.Failures),
		"failures":       e.Failures,
		"local_port":     e.LocalPort,
		"gateway":        e.Gateway,
	}
}

func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func loadConfig() (config, error) {
	var c config
	h, e := os.UserHomeDir()
	if e != nil {
		return c, nil
	}
	path := filepath.Join(h, ".tunlease.yaml")
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return c, nil
	}
	if e != nil {
		return c, fmt.Errorf("%s: %w", path, e)
	}
	if strings.TrimSpace(string(b)) == "" {
		return c, nil
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(b)))
	decoder.KnownFields(true)
	if e := decoder.Decode(&c); e != nil {
		return config{}, fmt.Errorf("%s: %w", path, e)
	}
	if c.DefaultScheme != "" && c.DefaultScheme != "http" && c.DefaultScheme != "https" {
		return config{}, fmt.Errorf("%s: default_scheme must be http or https", path)
	}
	return c, nil
}
