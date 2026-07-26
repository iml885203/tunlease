package cliapp

import (
	"errors"
	"fmt"
	"os"

	"github.com/iml885203/tunlease/pkg/tunnelcli"
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

	var claimFlags tunnelcli.ClaimFlags
	var detach, daemon bool
	claimCmd := &cobra.Command{
		Use:     "claim PATH [PATH...] --to PORT",
		Short:   "Temporarily forward callback path(s) to localhost",
		GroupID: "developer",
		Example: `  # Forward one exact path
  tul claim '/webhooks/provider' -p 8080 -g callbacks.example.com

  # /foo/* matches /foo/bar, but not /foo or /foo/bar/baz
  tul claim '/foo/*' -p 8080

  # /foo/** matches /foo and every descendant
  tul claim '/foo/**' -p 8080

  # Claim the base path and exactly one child level
  tul claim '/foo' '/foo/*' -p 8080`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(output); err != nil {
				return err
			}
			ui := newConsoleOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), output)
			options, err := claimFlags.Options(args)
			if err != nil {
				return err
			}
			gateway, token, insecure = options.Gateway, options.Token, options.Insecure
			c, e := get()
			if e != nil {
				return e
			}
			if e := checkLocalTarget(options.To); e != nil {
				if ui.json {
					ui.emitJSON(ui.err, map[string]any{
						"type": "warning", "code": "local_unavailable",
						"message": fmt.Sprintf("localhost:%d is not accepting connections; start the service to receive requests", options.To),
					})
				} else {
					ui.warning("WARNING: localhost:%d is not accepting connections; start the service to receive requests", options.To)
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
				return runDetach(ui, options.Paths, options.To, gateway, c.Gateway(), token, insecure || os.Getenv("TUNLEASE_INSECURE") != "", scheme)
			}
			return runClaim(ui, c, options.Paths, options.To, daemon)
		},
	}
	tunnelcli.BindClaimFlags(claimCmd, &claimFlags)
	claimCmd.Flags().BoolVarP(&detach, "detach", "d", false, "run in the background and return immediately (stop with tul release)")
	claimCmd.Flags().BoolVar(&daemon, "_daemon", false, "")
	_ = claimCmd.Flags().MarkHidden("_daemon")
	_ = claimCmd.MarkFlagRequired("to")
	claimCmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
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
