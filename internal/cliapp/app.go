package cliapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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

func NewCommand() *cobra.Command {
	return NewCommandWithVersion("dev", "unknown")
}

func NewCommandWithVersion(version, buildTime string) *cobra.Command {
	var gateway, token string
	var insecure bool
	root := &cobra.Command{Use: "tunle", Short: "Claim a 3rd-party callback path and tunnel it to your machine", SilenceUsage: true, Version: fmt.Sprintf("%s (%s)", version, buildTime)}
	// Client flags live on the client subcommands (claim/list/release), not on
	// root, so the gateway subcommand doesn't inherit irrelevant client flags.
	addClientFlags := func(c *cobra.Command) {
		c.Flags().StringVar(&gateway, "gateway", "", "gateway base URL (env TUNLEASE_GATEWAY)")
		c.Flags().StringVar(&token, "token", "", "API token (env TUNLEASE_TOKEN)")
		c.Flags().BoolVar(&insecure, "insecure", false, "skip gateway TLS verification, e.g. a self-signed gateway (env TUNLEASE_INSECURE)")
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
			return nil, errors.New("gateway is required (flag, TUNLEASE_GATEWAY, or ~/.tunlease.yaml)")
		}
		insec := insecure || c.Insecure || os.Getenv("TUNLEASE_INSECURE") != ""
		scheme := c.DefaultScheme
		if v := os.Getenv("TUNLEASE_DEFAULT_SCHEME"); v != "" {
			scheme = v
		}
		return tunnelclient.New(tunnelclient.Config{Gateway: c.Gateway, Token: c.Token, Insecure: insec, DefaultScheme: scheme})
	}

	var to int
	var detach, daemon bool
	claimCmd := &cobra.Command{
		Use:   "claim PATH [PATH...] --to PORT",
		Short: "Open a tunnel that exclusively owns path(s) until it closes",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := make([]string, 0, len(args))
			for _, p := range args {
				n, e := NormalizePath(p)
				if e != nil {
					return fmt.Errorf("%q: %w", p, e)
				}
				paths = append(paths, n)
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
				return runDetach(paths, to, gateway, token, insecure || os.Getenv("TUNLEASE_INSECURE") != "", scheme)
			}
			c, e := get()
			if e != nil {
				return e
			}
			return runClaim(c, paths, to, daemon)
		},
	}
	claimCmd.Flags().IntVar(&to, "to", 0, "local port to receive the traffic")
	claimCmd.Flags().BoolVar(&detach, "detach", false, "run in the background and return immediately (stop with tunle release)")
	claimCmd.Flags().BoolVar(&daemon, "_daemon", false, "")
	_ = claimCmd.Flags().MarkHidden("_daemon")
	_ = claimCmd.MarkFlagRequired("to")
	addClientFlags(claimCmd)
	root.AddCommand(claimCmd)

	var all bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Show your claims (--all shows everyone's)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, e := get()
			if e != nil {
				return e
			}
			return runList(cmd.Context(), c, all)
		},
	}
	listCmd.Flags().BoolVar(&all, "all", false, "show all owners' claims")
	addClientFlags(listCmd)
	root.AddCommand(listCmd)

	var relTo int
	releaseCmd := &cobra.Command{
		Use:   "release [PATH]",
		Short: "Release a claim by path, or everything on a local port with --to",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return runRelease(cmd.Context(), c, args, relTo)
		},
	}
	releaseCmd.Flags().IntVar(&relTo, "to", 0, "release all claims tunnelled to this local port")
	addClientFlags(releaseCmd)
	root.AddCommand(releaseCmd)

	root.AddCommand(newGatewayCommand())
	return root
}

func runClaim(c *tunnelclient.Client, paths []string, to int, daemon bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	session, e := c.Start(ctx, paths, to)
	if e != nil {
		return e
	}
	cl := session.Claim()
	pid := 0
	if daemon {
		pid = os.Getpid() // recorded so `tunle release` can stop this daemon
	}
	st := loadState()
	st.add(stateClaim{ClaimID: cl.ID, Gateway: c.Gateway(), Paths: cl.Paths, To: to, PID: pid})
	saveState(st)
	defer cleanupSessionState(c.Gateway(), to, cl.Paths)

	fmt.Printf("claimed %s (claim %s)\n", strings.Join(cl.Paths, " "), shortID(cl.ID))
	fmt.Printf("WARNING: real 3rd-party traffic for these paths now flows to localhost:%d\n", to)
	fmt.Println("tunnel connected  (Ctrl+C to release)")
	for {
		select {
		case <-ctx.Done():
			_ = session.Close()
			fmt.Println("\nreleased, tunnel closed")
			return nil
		case event, ok := <-session.Events():
			if !ok {
				if err := session.Err(); err != nil {
					return err
				}
				return nil
			}
			if event.Type == tunnelclient.EventTunnelReconnected {
				fmt.Println("tunnel reconnected")
				previous := cl
				cl = event.Claim
				st := loadState()
				st.removeSession(c.Gateway(), to, previous.Paths)
				st.add(stateClaim{ClaimID: cl.ID, Gateway: c.Gateway(), Paths: cl.Paths, To: to, PID: pid})
				saveState(st)
				fmt.Printf("paths remain claimed as %s\n", shortID(cl.ID))
			}
		}
	}
}

func cleanupSessionState(gateway string, to int, paths []string) {
	st := loadState()
	st.removeSession(gateway, to, paths)
	saveState(st)
}

func runList(ctx context.Context, c *tunnelclient.Client, all bool) error {
	st := loadState()
	mine := map[string]stateClaim{}
	for _, s := range st.Claims {
		mine[s.ClaimID] = s
	}
	claims, e := c.List(ctx)
	if e != nil {
		return e
	}
	shown := 0
	for _, x := range claims {
		s, own := mine[x.ID]
		if !all && !own {
			continue
		}
		target := "owner=" + x.Owner
		suffix := ""
		if own {
			target = fmt.Sprintf("→ localhost:%d", s.To)
			suffix = "  (you)"
		}
		fmt.Printf("%-40s %-24s connected %s%s\n", strings.Join(x.Paths, ","), target, x.StartedAt.Local().Format("15:04:05"), suffix)
		shown++
	}
	if shown == 0 {
		if all {
			fmt.Println("no active claims")
		} else {
			fmt.Println("no active claims of yours (use --all to see everyone's)")
		}
	}
	return nil
}

func runRelease(ctx context.Context, c *tunnelclient.Client, args []string, relTo int) error {
	st := loadState()
	if relTo > 0 {
		released := 0
		for _, s := range st.Claims {
			if s.To == relTo {
				if e := c.Release(ctx, s.ClaimID); e != nil {
					return e
				}
				stopDaemon(s) // stop the background daemon, if this was --detach
				st.removeByID(s.ClaimID)
				fmt.Printf("released %s\n", strings.Join(s.Paths, " "))
				released++
			}
		}
		saveState(st)
		if released == 0 {
			fmt.Printf("no claims recorded for local port %d on this machine\n", relTo)
		}
		return nil
	}
	target, e := NormalizePath(args[0])
	if e != nil {
		return e
	}
	// 先查本機 state，找不到再翻遠端（可能是別台機器留下的租約）
	for _, s := range st.Claims {
		if contains(s.Paths, target) {
			if e := c.Release(ctx, s.ClaimID); e != nil {
				return e
			}
			stopDaemon(s) // stop the background daemon, if this was --detach
			st.removeByID(s.ClaimID)
			saveState(st)
			fmt.Printf("released %s\n", target)
			return nil
		}
	}
	claims, e := c.List(ctx)
	if e != nil {
		return e
	}
	for _, x := range claims {
		if contains(x.Paths, target) {
			if e := c.Release(ctx, x.ID); e != nil {
				return e
			}
			fmt.Printf("released %s\n", target)
			return nil
		}
	}
	return fmt.Errorf("no active claim found for %s", target)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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
