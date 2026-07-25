package cliapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

func TestLocalTargetErrorRemovesDialInternals(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: errors.New("connection refused")},
	}
	if got, want := localTargetError(err), "connection refused"; got != want {
		t.Fatalf("localTargetError() = %q, want %q", got, want)
	}
}

func TestConnectionMessage(t *testing.T) {
	if got, want := connectionMessage("Connected", []string{"/callback"}, 8080, nil), "Connected: /callback → localhost:8080"; got != want {
		t.Fatalf("connectionMessage() = %q, want %q", got, want)
	}
	expiresAt := time.Date(2026, time.July, 25, 18, 48, 13, 0, time.Local)
	if got, want := connectionMessage("Connected", []string{"/callback"}, 8080, &expiresAt), "Connected until 18:48:13: /callback → localhost:8080"; got != want {
		t.Fatalf("connectionMessage() = %q, want %q", got, want)
	}
}

func TestFormatActivityDuration(t *testing.T) {
	tests := map[time.Duration]string{
		0:                       "<1ms",
		500 * time.Microsecond:  "<1ms",
		1500 * time.Microsecond: "2ms",
		1250 * time.Millisecond: "1.25s",
	}
	for duration, want := range tests {
		if got := formatActivityDuration(duration); got != want {
			t.Errorf("formatActivityDuration(%s) = %q, want %q", duration, got, want)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{"/webhooks/provider/callback/getbalance": "/webhooks/provider/callback/getbalance", "/webhooks/provider/callback/updatebalance": "/webhooks/provider/callback/updatebalance", "/x/": "/x", "/x/*": "/x/*", "/x/**": "/x/**"}
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
	if _, e := NormalizePath("/x/**/y"); e == nil {
		t.Fatal("accepted inner recursive wildcard")
	}
}

func TestCommandName(t *testing.T) {
	if got := NewCommandWithVersion("dev", "unknown").Use; got != "tul" {
		t.Fatalf("command name = %q, want %q", got, "tul")
	}
}

func TestHelpShowsHappyPathAndSeparatesOperatorCommand(t *testing.T) {
	command := NewCommandWithVersion("dev", "unknown")
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Developer commands:",
		"Platform operator commands:",
		"tul claim '/webhooks/provider' -p 8080 -g callbacks.example.com",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not contain %q:\n%s", want, out.String())
		}
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
	claim, _, err := NewCommandWithVersion("dev", "unknown").Find([]string{"claim"})
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

func TestCommonFlagsHaveConsistentShorthands(t *testing.T) {
	tests := []struct {
		command, flag, shorthand string
	}{
		{"claim", "to", "p"},
		{"claim", "detach", "d"},
		{"claim", "gateway", "g"},
		{"claim", "token", "t"},
		{"claim", "insecure", "k"},
		{"claim", "output", "o"},
		{"list", "all", "a"},
		{"list", "gateway", "g"},
		{"list", "output", "o"},
		{"release", "to", "p"},
		{"release", "gateway", "g"},
		{"release", "output", "o"},
		{"gateway", "config", "c"},
	}
	root := NewCommandWithVersion("dev", "unknown")
	for _, tt := range tests {
		command, _, err := root.Find([]string{tt.command})
		if err != nil {
			t.Fatal(err)
		}
		flag := command.Flags().Lookup(tt.flag)
		if flag == nil {
			t.Errorf("%s --%s is missing", tt.command, tt.flag)
			continue
		}
		if flag.Shorthand != tt.shorthand {
			t.Errorf("%s --%s shorthand = %q, want %q", tt.command, tt.flag, flag.Shorthand, tt.shorthand)
		}
	}
}

func TestJSONCommandErrorHasStableCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TUNLEASE_GATEWAY", "")
	command := NewCommandWithVersion("dev", "unknown")
	command.SetArgs([]string{"list", "--output", "json"})
	executed, err := command.ExecuteC()
	if err == nil {
		t.Fatal("expected an error")
	}
	var stderr bytes.Buffer
	PrintCommandError(&stderr, executed, []string{"list", "--output", "json"}, err)
	var event map[string]any
	if decodeErr := json.Unmarshal(stderr.Bytes(), &event); decodeErr != nil {
		t.Fatalf("JSON error = %q: %v", stderr.String(), decodeErr)
	}
	if event["schema_version"] != float64(1) || event["type"] != "error" || event["code"] != "command_failed" {
		t.Fatalf("JSON error = %#v", event)
	}
}

func TestJSONParseErrorsIgnoreOutputFlagOrder(t *testing.T) {
	tests := [][]string{
		{"list", "--bad", "--output", "json"},
		{"list", "--output", "json", "--bad"},
		{"list", "--bad", "--output=json"},
		{"list", "--bad", "-o=json"},
		{"list", "--bad", "-ojson"},
		{"list", "-ojson", "--bad"},
	}
	for _, args := range tests {
		command := NewCommandWithVersion("dev", "unknown")
		command.SetArgs(args)
		executed, err := command.ExecuteC()
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
		var stderr bytes.Buffer
		PrintCommandError(&stderr, executed, args, err)
		var event map[string]any
		if decodeErr := json.Unmarshal(stderr.Bytes(), &event); decodeErr != nil {
			t.Fatalf("%v error is not JSON: %q: %v", args, stderr.String(), decodeErr)
		}
		if event["type"] != "error" || event["code"] != "command_failed" {
			t.Fatalf("%v JSON error = %#v", args, event)
		}
	}
}

func TestJSONListIsOneStructuredDocument(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"claims": []map[string]any{{
			"claim_id": "mine", "owner": "dev", "paths": []string{"/x"}, "started_at": now,
		}}})
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	saveState(state{Claims: []stateClaim{{ClaimID: "mine", Paths: []string{"/x"}, To: 8080}}})
	var out, stderr bytes.Buffer
	if err = runList(newConsoleOutput(&out, &stderr, "json"), context.Background(), client, false); err != nil {
		t.Fatal(err)
	}
	var event struct {
		SchemaVersion int              `json:"schema_version"`
		Type          string           `json:"type"`
		Claims        []map[string]any `json:"claims"`
	}
	if err = json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatalf("JSON list = %q: %v", out.String(), err)
	}
	if event.SchemaVersion != 1 || event.Type != "claim_list" || len(event.Claims) != 1 ||
		event.Claims[0]["target"] != "localhost:8080" || event.Claims[0]["local_port"] != float64(8080) {
		t.Fatalf("JSON list = %#v", event)
	}
}

func TestFindDaemonClaimMatchesGatewayAndNewProcessPID(t *testing.T) {
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	saveState(state{Claims: []stateClaim{
		{ClaimID: "other-gateway", Gateway: "https://other/_tunlease", Paths: []string{"/x"}, To: 8080, PID: 42},
		{ClaimID: "old-process", Gateway: "https://gateway/_tunlease", Paths: []string{"/x"}, To: 8080, PID: 41},
		{ClaimID: "current", Gateway: "https://gateway/_tunlease", Paths: []string{"/x"}, To: 8080, PID: 42},
	}})

	got, ok := findDaemonClaim("https://gateway/_tunlease", 8080, []string{"/x"}, 42)
	if !ok || got.ClaimID != "current" {
		t.Fatalf("findDaemonClaim() = %#v, %v", got, ok)
	}
}

func TestReleaseByPortPersistsSuccessBeforePartialFailure(t *testing.T) {
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ok") || strings.HasSuffix(r.URL.Path, "/ok2") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"internal_error","detail":"try again"}`))
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	saveState(state{Claims: []stateClaim{
		{ClaimID: "ok", Gateway: client.Gateway(), Paths: []string{"/ok"}, To: 8080},
		{ClaimID: "fail", Gateway: client.Gateway(), Paths: []string{"/fail"}, To: 8080},
		{ClaimID: "ok2", Gateway: client.Gateway(), Paths: []string{"/ok2"}, To: 8080},
		{ClaimID: "other-gateway", Gateway: "https://other/_tunlease", Paths: []string{"/other"}, To: 8080},
	}})
	var out, stderr bytes.Buffer
	err = runRelease(newConsole(&out, &stderr), context.Background(), client, nil, 8080)
	var partial *partialReleaseError
	if !errors.As(err, &partial) || partial.Released != 2 || len(partial.Failures) != 1 {
		t.Fatalf("runRelease() error = %#v", err)
	}
	claims := loadState().Claims
	if len(claims) != 2 || claims[0].ClaimID != "fail" || claims[1].ClaimID != "other-gateway" {
		t.Fatalf("persisted claims = %#v", claims)
	}
	if !strings.Contains(out.String(), "released /ok") || !strings.Contains(out.String(), "released /ok2") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestReleaseByPathUsesSelectedGatewayState(t *testing.T) {
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	var releasedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releasedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	saveState(state{Claims: []stateClaim{
		{ClaimID: "other", Gateway: "https://other/_tunlease", Paths: []string{"/same"}, To: 8080},
		{ClaimID: "selected", Gateway: client.Gateway(), Paths: []string{"/same"}, To: 8080},
	}})
	var out, stderr bytes.Buffer
	if err = runRelease(newConsole(&out, &stderr), context.Background(), client, []string{"/same"}, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(releasedPath, "/selected") {
		t.Fatalf("released URL = %q", releasedPath)
	}
	claims := loadState().Claims
	if len(claims) != 1 || claims[0].ClaimID != "other" {
		t.Fatalf("persisted claims = %#v", claims)
	}
}

func TestCommandReturnsErrorsWithoutPrintingADuplicate(t *testing.T) {
	command := NewCommandWithVersion("dev", "unknown")
	var stderr bytes.Buffer
	command.SetErr(&stderr)
	command.SetArgs([]string{"claim", "/x", "--to", "0"})

	if err := command.Execute(); err == nil {
		t.Fatal("expected an error")
	}
	if stderr.Len() != 0 {
		t.Fatalf("Cobra printed a duplicate error: %q", stderr.String())
	}
}

func TestClaimReportsMissingGatewayBeforeLocalTargetWarning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TUNLEASE_GATEWAY", "")
	command := NewCommandWithVersion("dev", "unknown")
	var stderr bytes.Buffer
	command.SetErr(&stderr)
	command.SetArgs([]string{"claim", "/x", "--to", "1"})

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "gateway required") {
		t.Fatalf("error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("local warning printed before gateway error: %q", stderr.String())
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

	command := NewCommandWithVersion("dev", "unknown")
	command.SetArgs([]string{"list"})
	err := command.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "field gatewy not found") {
		t.Fatalf("error = %q", err)
	}
}
