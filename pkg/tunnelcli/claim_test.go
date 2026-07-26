package tunnelcli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestClaimFlagsOptionsPreservePathSemantics(t *testing.T) {
	flags := ClaimFlags{To: 8080, Gateway: "gateway.example", Token: "secret", Insecure: true}
	options, err := flags.Options([]string{"/foo/", "/foo/*", "/foo/**"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/foo", "/foo/*", "/foo/**"}
	for i := range want {
		if options.Paths[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, options.Paths[i], want[i])
		}
	}
	if options.To != 8080 || options.Gateway != "gateway.example" || options.Token != "secret" || !options.Insecure {
		t.Fatalf("options = %#v", options)
	}
}

func TestClaimFlagsOptionsRejectInvalidPortAndPath(t *testing.T) {
	tests := []struct {
		name  string
		flags ClaimFlags
		paths []string
	}{
		{name: "missing port", flags: ClaimFlags{}, paths: []string{"/foo"}},
		{name: "missing path", flags: ClaimFlags{To: 8080}},
		{name: "root path", flags: ClaimFlags{To: 8080}, paths: []string{"/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.flags.Options(tt.paths); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBindClaimFlagsUsesTulAliases(t *testing.T) {
	cmd := &cobra.Command{Use: "claim"}
	var flags ClaimFlags
	BindClaimFlags(cmd, &flags)

	if err := cmd.ParseFlags([]string{"-p", "8080", "-g", "gateway.example", "-t", "secret", "-k"}); err != nil {
		t.Fatal(err)
	}
	if flags.To != 8080 || flags.Gateway != "gateway.example" || flags.Token != "secret" || !flags.Insecure {
		t.Fatalf("flags = %#v", flags)
	}
}

func TestClaimFlagsOptionsUseEnvironmentFallbacks(t *testing.T) {
	t.Setenv("TUNLEASE_GATEWAY", "env-gateway.example")
	t.Setenv("TUNLEASE_TOKEN", "env-secret")
	t.Setenv("TUNLEASE_INSECURE", "1")

	options, err := (ClaimFlags{To: 8080}).Options([]string{"/foo"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Gateway != "env-gateway.example" || options.Token != "env-secret" || !options.Insecure {
		t.Fatalf("options = %#v", options)
	}
}

func TestClaimFlagsOverrideEnvironment(t *testing.T) {
	t.Setenv("TUNLEASE_GATEWAY", "env-gateway.example")
	t.Setenv("TUNLEASE_TOKEN", "env-secret")

	options, err := (ClaimFlags{
		To: 8080, Gateway: "flag-gateway.example", Token: "flag-secret",
	}).Options([]string{"/foo"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Gateway != "flag-gateway.example" || options.Token != "flag-secret" {
		t.Fatalf("options = %#v", options)
	}
}
