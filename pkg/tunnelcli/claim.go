// Package tunnelcli provides reusable command-line semantics for applications
// that embed the Tunlease client.
package tunnelcli

import (
	"errors"
	"fmt"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
	"github.com/spf13/cobra"
)

// ClaimFlags holds the flags shared by Tunlease claim commands.
type ClaimFlags struct {
	To       int
	Gateway  string
	Token    string
	Insecure bool
}

// ClaimOptions is a validated claim request. Paths retain Tunlease's exact,
// one-level wildcard, and recursive wildcard semantics.
type ClaimOptions struct {
	Paths    []string
	To       int
	Gateway  string
	Token    string
	Insecure bool
}

// BindClaimFlags adds Tunlease's shared claim flags to cmd.
func BindClaimFlags(cmd *cobra.Command, flags *ClaimFlags) {
	cmd.Flags().IntVarP(&flags.To, "to", "p", 0, "local port to receive the traffic")
	cmd.Flags().StringVarP(&flags.Gateway, "gateway", "g", "", "gateway base URL (env TUNLEASE_GATEWAY)")
	cmd.Flags().StringVarP(&flags.Token, "token", "t", "", "API token (env TUNLEASE_TOKEN)")
	cmd.Flags().BoolVarP(&flags.Insecure, "insecure", "k", false, "skip gateway TLS verification, e.g. a self-signed gateway (env TUNLEASE_INSECURE)")
}

// Options validates the flags and canonicalizes paths without widening their
// matching behavior.
func (flags ClaimFlags) Options(paths []string) (ClaimOptions, error) {
	if flags.To < 1 || flags.To > 65535 {
		return ClaimOptions{}, errors.New("local port must be between 1 and 65535")
	}
	if len(paths) == 0 || len(paths) > tunnelclient.MaxPathsPerClaim {
		return ClaimOptions{}, fmt.Errorf("between 1 and %d paths are required", tunnelclient.MaxPathsPerClaim)
	}

	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		canonical, err := tunnelclient.NormalizePath(path)
		if err != nil {
			return ClaimOptions{}, fmt.Errorf("%q: %w", path, err)
		}
		normalized = append(normalized, canonical)
	}
	return ClaimOptions{
		Paths: normalized, To: flags.To, Gateway: flags.Gateway,
		Token: flags.Token, Insecure: flags.Insecure,
	}, nil
}
