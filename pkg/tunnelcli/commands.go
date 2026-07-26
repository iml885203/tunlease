package tunnelcli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const (
	ClaimUse     = "claim PATH [PATH...] --to PORT"
	ClaimShort   = "Temporarily forward callback path(s) to localhost"
	ListUse      = "list"
	ListShort    = "Show callback paths being forwarded"
	ReleaseUse   = "release [PATH]"
	ReleaseShort = "Stop forwarding a claimed path"
)

// ClaimExample returns the canonical examples using the embedding command.
func ClaimExample(command string) string {
	return fmt.Sprintf(`  # Forward one exact path
  %[1]s '/webhooks/provider' -p 8080 -g callbacks.example.com

  # /foo/* matches /foo/bar, but not /foo or /foo/bar/baz
  %[1]s '/foo/*' -p 8080

  # /foo/** matches /foo and every descendant
  %[1]s '/foo/**' -p 8080

  # Claim the base path and exactly one child level
  %[1]s '/foo' '/foo/*' -p 8080`, command)
}

// ListFlags holds the flags shared by Tunlease list commands.
type ListFlags struct {
	All      bool
	Gateway  string
	Token    string
	Insecure bool
	Output   string
}

// BindListFlags adds Tunlease's list flags to cmd.
func BindListFlags(cmd *cobra.Command, flags *ListFlags) {
	cmd.Flags().BoolVarP(&flags.All, "all", "a", false, "show all owners' claims")
	bindClientFlags(cmd, &flags.Gateway, &flags.Token, &flags.Insecure, &flags.Output)
}

// Resolve applies environment fallbacks and validates output.
func (flags ListFlags) Resolve() (ListFlags, error) {
	flags.Gateway, flags.Token, flags.Insecure = resolveClient(flags.Gateway, flags.Token, flags.Insecure)
	if flags.Output == "" {
		flags.Output = "text"
	}
	return flags, ValidateOutput(flags.Output)
}

// ReleaseFlags holds the flags shared by Tunlease release commands.
type ReleaseFlags struct {
	To       int
	Gateway  string
	Token    string
	Insecure bool
	Output   string
}

// BindReleaseFlags adds Tunlease's release flags to cmd.
func BindReleaseFlags(cmd *cobra.Command, flags *ReleaseFlags) {
	cmd.Flags().IntVarP(&flags.To, "to", "p", 0, "release all claims tunnelled to this local port")
	bindClientFlags(cmd, &flags.Gateway, &flags.Token, &flags.Insecure, &flags.Output)
}

// Resolve applies environment fallbacks and validates output.
func (flags ReleaseFlags) Resolve() (ReleaseFlags, error) {
	flags.Gateway, flags.Token, flags.Insecure = resolveClient(flags.Gateway, flags.Token, flags.Insecure)
	if flags.Output == "" {
		flags.Output = "text"
	}
	return flags, ValidateOutput(flags.Output)
}

// ValidateOutput accepts the stable Tunlease output formats.
func ValidateOutput(output string) error {
	if output != "text" && output != "json" {
		return fmt.Errorf("output must be text or json, got %q", output)
	}
	return nil
}

func bindClientFlags(cmd *cobra.Command, gateway, token *string, insecure *bool, output *string) {
	cmd.Flags().StringVarP(gateway, "gateway", "g", "", "gateway base URL (env TUNLEASE_GATEWAY)")
	cmd.Flags().StringVarP(token, "token", "t", "", "API token (env TUNLEASE_TOKEN)")
	cmd.Flags().BoolVarP(insecure, "insecure", "k", false, "skip gateway TLS verification, e.g. a self-signed gateway (env TUNLEASE_INSECURE)")
	cmd.Flags().StringVarP(output, "output", "o", "text", "output format: text or json")
}

func resolveClient(gateway, token string, insecure bool) (string, string, bool) {
	if gateway == "" {
		gateway = os.Getenv("TUNLEASE_GATEWAY")
	}
	if token == "" {
		token = os.Getenv("TUNLEASE_TOKEN")
	}
	return gateway, token, insecure || os.Getenv("TUNLEASE_INSECURE") != ""
}
