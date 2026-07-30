package tunnelcli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestClaimExampleUsesEmbeddingCommand(t *testing.T) {
	example := ClaimExample("orbit tunnel claim")
	if !strings.Contains(example, "orbit tunnel claim '/foo/**' -p 8080") {
		t.Fatalf("example = %q", example)
	}
}

// An embedding CLI binds these flags and resolves them, so the short aliases and
// output validation are asserted the way that host reaches them.
func TestBindListFlagsUsesTulAliases(t *testing.T) {
	cmd := &cobra.Command{Use: ListUse}
	var flags ListFlags
	BindListFlags(cmd, &flags)
	if err := cmd.ParseFlags([]string{"-a", "-g", "gateway.example", "-t", "secret", "-k", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	resolved, err := flags.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.All || resolved.Gateway != "gateway.example" || resolved.Token != "secret" ||
		!resolved.Insecure || resolved.Output != "json" {
		t.Fatalf("flags = %#v", resolved)
	}
}

func TestBindReleaseFlagsUsesTulAliases(t *testing.T) {
	cmd := &cobra.Command{Use: ReleaseUse}
	var flags ReleaseFlags
	BindReleaseFlags(cmd, &flags)
	if err := cmd.ParseFlags([]string{"-p", "8080", "-g", "gateway.example", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	resolved, err := flags.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.To != 8080 || resolved.Gateway != "gateway.example" || resolved.Output != "json" {
		t.Fatalf("flags = %#v", resolved)
	}
}

// Resolve is where an unknown --output has to be rejected; reaching it through
// the flags keeps the check on the path every embedding command takes.
func TestResolveRejectsUnknownOutputFormat(t *testing.T) {
	for _, tt := range []struct {
		output   string
		accepted bool
	}{
		{"text", true},
		{"json", true},
		{"yaml", false},
		{"xml", false},
	} {
		t.Run(tt.output, func(t *testing.T) {
			cmd := &cobra.Command{Use: ListUse}
			var flags ListFlags
			BindListFlags(cmd, &flags)
			if err := cmd.ParseFlags([]string{"-o", tt.output}); err != nil {
				t.Fatal(err)
			}
			_, err := flags.Resolve()
			if accepted := err == nil; accepted != tt.accepted {
				t.Errorf("Resolve() with --output %s accepted = %v, want %v", tt.output, accepted, tt.accepted)
			}
		})
	}
}
