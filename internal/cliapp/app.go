package cliapp

import (
	"errors"
	"fmt"
	"os"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
	"github.com/spf13/cobra"
)

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
