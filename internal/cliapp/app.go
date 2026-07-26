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
	var daemon bool
	claimCmd := &cobra.Command{
		Use:     tunnelcli.ClaimUse,
		Short:   tunnelcli.ClaimShort,
		GroupID: "developer",
		Example: tunnelcli.ClaimExample("tul claim"),
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options, err := claimFlags.Options(args)
			if err != nil {
				return err
			}
			gateway, token, insecure, output = options.Gateway, options.Token, options.Insecure, options.Output
			ui := newConsoleOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), output)
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
			if options.Detach {
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
	claimCmd.Flags().BoolVar(&daemon, "_daemon", false, "")
	_ = claimCmd.Flags().MarkHidden("_daemon")
	_ = claimCmd.MarkFlagRequired("to")
	root.AddCommand(claimCmd)

	var listFlags tunnelcli.ListFlags
	listCmd := &cobra.Command{
		Use:     tunnelcli.ListUse,
		Short:   tunnelcli.ListShort,
		GroupID: "developer",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := listFlags.Resolve()
			if err != nil {
				return err
			}
			gateway, token, insecure, output = resolved.Gateway, resolved.Token, resolved.Insecure, resolved.Output
			c, e := get()
			if e != nil {
				return e
			}
			return runList(newConsoleOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), output), cmd.Context(), c, resolved.All)
		},
	}
	tunnelcli.BindListFlags(listCmd, &listFlags)
	root.AddCommand(listCmd)

	var releaseFlags tunnelcli.ReleaseFlags
	releaseCmd := &cobra.Command{
		Use:     tunnelcli.ReleaseUse,
		Short:   tunnelcli.ReleaseShort,
		GroupID: "developer",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := releaseFlags.Resolve()
			if err != nil {
				return err
			}
			gateway, token, insecure, output = resolved.Gateway, resolved.Token, resolved.Insecure, resolved.Output
			c, e := get()
			if e != nil {
				return e
			}
			if resolved.To > 0 && len(args) > 0 {
				return errors.New("specify either PATH or --to, not both")
			}
			if resolved.To == 0 && len(args) == 0 {
				return errors.New("specify a PATH or --to PORT")
			}
			return runRelease(newConsoleOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), output), cmd.Context(), c, args, resolved.To)
		},
	}
	tunnelcli.BindReleaseFlags(releaseCmd, &releaseFlags)
	root.AddCommand(releaseCmd)

	gatewayCmd := newGatewayCommand()
	gatewayCmd.GroupID = "operator"
	root.AddCommand(gatewayCmd)
	return root
}
