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

func TestValidateOutputRejectsUnknownFormat(t *testing.T) {
	if err := ValidateOutput("yaml"); err == nil {
		t.Fatal("expected output validation error")
	}
}
