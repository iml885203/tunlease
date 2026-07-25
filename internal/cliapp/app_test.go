package cliapp

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{"/webhooks/provider/callback/getbalance": "/webhooks/provider/callback/getbalance/*", "/webhooks/provider/callback/updatebalance": "/webhooks/provider/callback/updatebalance/*", "/x/": "/x/*", "/x/*": "/x/*"}
	for in, want := range cases {
		got, e := NormalizePath(in)
		if e != nil || got != want {
			t.Fatalf("%q => %q,%v", in, got, e)
		}
	}
	if _, e := NormalizePath("x"); e == nil {
		t.Fatal("accepted relative path")
	}
	if _, e := NormalizePath("/x/*/y"); e == nil {
		t.Fatal("accepted inner wildcard")
	}
}

func TestCheckLocalTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, rawPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkLocalTarget(port); err != nil {
		t.Fatalf("listening target reported unavailable: %v", err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err = checkLocalTarget(port); err == nil {
		t.Fatal("closed target reported available")
	}
}

func TestClaimDetachShorthand(t *testing.T) {
	claim, _, err := NewCommand().Find([]string{"claim"})
	if err != nil {
		t.Fatal(err)
	}
	flag := claim.Flags().Lookup("detach")
	if flag == nil {
		t.Fatal("detach flag is missing")
	}
	if flag.Shorthand != "d" {
		t.Fatalf("detach shorthand = %q, want %q", flag.Shorthand, "d")
	}
}

func TestLoadConfigMissingIsValid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got != (config{}) {
		t.Fatalf("config = %#v", got)
	}
}

func TestLoadConfigValid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	content := []byte("gateway: callbacks.example.com\ntoken: secret\ninsecure: true\ndefault_scheme: http\n")
	if err := os.WriteFile(filepath.Join(home, ".tunlease.yaml"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := config{Gateway: "callbacks.example.com", Token: "secret", Insecure: true, DefaultScheme: "http"}
	if got != want {
		t.Fatalf("config = %#v, want %#v", got, want)
	}
}

func TestLoadConfigReportsInvalidFile(t *testing.T) {
	tests := map[string]string{
		"unknown field":  "gatewy: callbacks.example.com\n",
		"invalid type":   "insecure: sometimes\n",
		"invalid scheme": "default_scheme: ftp\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".tunlease.yaml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadConfig()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q does not identify %s", err, path)
			}
		})
	}
}

func TestClientCommandReportsConfigErrorBeforeConnecting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".tunlease.yaml")
	if err := os.WriteFile(path, []byte("gatewy: callbacks.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := NewCommand()
	command.SetArgs([]string{"list"})
	err := command.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "field gatewy not found") {
		t.Fatalf("error = %q", err)
	}
}
