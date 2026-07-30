package cliapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// A session ends quietly only when the gateway says the claim expired or was
// released; anything else is an error the user has to see. finishTerminalSession
// is where that split becomes output, so it is asserted there.
func TestOnlyExpectedTerminalReasonsEndASessionQuietly(t *testing.T) {
	claim := tunnelclient.Claim{Paths: []string{"/callback"}}
	for _, tt := range []struct {
		name     string
		err      error
		quiet    bool
		contains string
	}{
		{name: "expired claim", err: &tunnelclient.APIError{Code: "claim_expired"}, quiet: true, contains: "Claim expired"},
		{name: "released claim", err: &tunnelclient.APIError{Code: "claim_released"}, quiet: true, contains: "Released"},
		{name: "unauthorized", err: &tunnelclient.APIError{Code: "unauthorized"}},
		{name: "transport failure", err: errors.New("connection lost")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			err := finishTerminalSession(newConsole(&out, &stderr), tt.err, claim)
			if quiet := err == nil; quiet != tt.quiet {
				t.Fatalf("finishTerminalSession() = %v, want quiet = %v", err, tt.quiet)
			}
			if tt.contains != "" && !strings.Contains(out.String(), tt.contains) {
				t.Errorf("output %q does not mention %q", out.String(), tt.contains)
			}
		})
	}
}

// An expiry names the time the claim ran out, in both output formats, and reports
// nothing on stderr because it is not a failure.
func TestExpiryNamesItsTime(t *testing.T) {
	expiresAt := time.Date(2026, time.July, 25, 21, 30, 0, 0, time.Local)
	claim := tunnelclient.Claim{Paths: []string{"/callback"}, ExpiresAt: &expiresAt}
	expiryErr := &tunnelclient.APIError{Code: "claim_expired", Detail: "maximum duration reached"}

	var textOut, textErr bytes.Buffer
	if err := finishTerminalSession(newConsole(&textOut, &textErr), expiryErr, claim); err != nil {
		t.Fatalf("finishTerminalSession() = %v", err)
	}
	if got, want := textOut.String(), "Claim expired at 21:30:00.\n"; got != want {
		t.Errorf("text expiry = %q, want %q", got, want)
	}
	if textErr.Len() != 0 {
		t.Errorf("text expiry stderr = %q", textErr.String())
	}

	var jsonOut, jsonErr bytes.Buffer
	if err := finishTerminalSession(newConsoleOutput(&jsonOut, &jsonErr, "json"), expiryErr, claim); err != nil {
		t.Fatalf("finishTerminalSession() = %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "expired" || event["expired_at"] == nil {
		t.Errorf("JSON expiry = %#v", event)
	}
	if jsonErr.Len() != 0 {
		t.Errorf("JSON expiry stderr = %q", jsonErr.String())
	}
}

func TestPrintStoppedTerminalEmitsOneLifecycleRecord(t *testing.T) {
	var out, stderr bytes.Buffer
	printStoppedTerminal(newConsoleOutput(&out, &stderr, "json"), []string{"/callback"})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("terminal records = %d, want 1: %q", len(lines), out.String())
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "released" || stderr.Len() != 0 {
		t.Fatalf("event=%#v stderr=%q", event, stderr.String())
	}
}

// Request durations and dial failures are rendered into the claim session's
// activity and warning lines. The event loop that renders them cannot be made to
// produce a chosen duration or syscall error, so the display rules are enumerated
// directly: sub-millisecond requests read as "<1ms", and a dial error is reduced to
// its cause without leaking the dial internals around it.
func TestActivityLinesRenderDurationsAndDialFailures(t *testing.T) {
	for _, tt := range []struct {
		duration time.Duration
		want     string
	}{
		{0, "<1ms"},
		{500 * time.Microsecond, "<1ms"},
		{1500 * time.Microsecond, "2ms"},
		{1250 * time.Millisecond, "1.25s"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatActivityDuration(tt.duration); got != tt.want {
				t.Errorf("a %s request reads as %q, want %q", tt.duration, got, tt.want)
			}
		})
	}

	t.Run("dial failure keeps only its cause", func(t *testing.T) {
		err := &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: &os.SyscallError{Syscall: "connect", Err: errors.New("connection refused")},
		}
		if got, want := localTargetError(err), "connection refused"; got != want {
			t.Errorf("dial failure reads as %q, want %q", got, want)
		}
	})
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

func TestClaimHelpExplainsWildcardBoundaries(t *testing.T) {
	command := NewCommandWithVersion("dev", "unknown")
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"claim", "--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/foo/* matches /foo/bar, but not /foo or /foo/bar/baz",
		"/foo/** matches /foo and every descendant",
		"tul claim '/foo' '/foo/*' -p 8080",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("claim help does not contain %q:\n%s", want, out.String())
		}
	}
}

// Claiming warns when nothing is listening on the target port, so the developer
// learns the tunnel will have nowhere to forward to. The warning is advisory: the
// claim still proceeds to the gateway.
func TestClaimWarnsWhenTheLocalTargetIsUnreachable(t *testing.T) {
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

	// An unreachable gateway stops the claim right after the local-target check, so
	// the warning is the only thing the check contributes to stderr.
	claim := func(t *testing.T) string {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
		command := NewCommandWithVersion("dev", "unknown")
		var stderr bytes.Buffer
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&stderr)
		command.SetArgs([]string{
			"claim", "/x", "--to", strconv.Itoa(port),
			"--gateway", "127.0.0.1:1", "--insecure",
		})
		_ = command.Execute()
		return stderr.String()
	}

	if got := claim(t); strings.Contains(got, "Could not reach localhost") {
		t.Errorf("a listening target was reported unreachable: %q", got)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if got := claim(t); !strings.Contains(got, fmt.Sprintf("localhost:%d", port)) {
		t.Errorf("a closed target produced no warning: %q", got)
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

func TestListExplainsDisabledClaimList(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = runList(newConsole(&bytes.Buffer{}, &bytes.Buffer{}), context.Background(), client, false)
	var unavailable *claimListUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("runList() error = %v", err)
	}
	var stderr bytes.Buffer
	PrintError(&stderr, err)
	for _, want := range []string{"claim_list_unavailable", "does not expose claim discovery"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("printed error %q does not mention %q", stderr.String(), want)
		}
	}
}

// Detaching waits for the daemon it just spawned to record its claim. Only the
// entry matching the gateway, port, paths and that new process counts, so a stale
// entry from a previous run is never mistaken for it. Reaching this through the CLI
// would require spawning a real daemon, so the matching rule is asserted directly.
func TestDetachRecognisesOnlyItsOwnDaemonClaim(t *testing.T) {
	const gateway = "https://gateway/_tunlease"
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	saveState(state{Claims: []stateClaim{
		{ClaimID: "other-gateway", Gateway: "https://other/_tunlease", Paths: []string{"/x"}, To: 8080, PID: 42},
		{ClaimID: "old-process", Gateway: gateway, Paths: []string{"/x"}, To: 8080, PID: 41},
		{ClaimID: "current", Gateway: gateway, Paths: []string{"/x"}, To: 8080, PID: 42},
	}})

	for _, tt := range []struct {
		name    string
		gateway string
		to      int
		paths   []string
		pid     int
		wantID  string
	}{
		{name: "the claim this daemon recorded", gateway: gateway, to: 8080, paths: []string{"/x"}, pid: 42, wantID: "current"},
		{name: "another gateway", gateway: "https://elsewhere/_tunlease", to: 8080, paths: []string{"/x"}, pid: 42},
		{name: "another port", gateway: gateway, to: 9090, paths: []string{"/x"}, pid: 42},
		{name: "another path", gateway: gateway, to: 8080, paths: []string{"/y"}, pid: 42},
		{name: "a process that is not the new daemon", gateway: gateway, to: 8080, paths: []string{"/x"}, pid: 99},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findDaemonClaim(tt.gateway, tt.to, tt.paths, tt.pid)
			if tt.wantID == "" {
				if ok {
					t.Errorf("matched %q, want no match", got.ClaimID)
				}
				return
			}
			if !ok || got.ClaimID != tt.wantID {
				t.Errorf("matched %#v, %v; want %q", got, ok, tt.wantID)
			}
		})
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
	if !strings.Contains(out.String(), "Released: /ok") || !strings.Contains(out.String(), "Released: /ok2") {
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

func TestReleaseMissingPathIsIdempotent(t *testing.T) {
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"claims":[]}`))
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err = runRelease(newConsole(&out, &stderr), context.Background(), client, []string{"/missing"}, 0); err != nil {
		t.Fatalf("runRelease() = %v", err)
	}
	if !strings.Contains(out.String(), "No active claim: /missing") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestReleaseStaleLocalStateIsIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"claim_not_found","detail":"claim no longer exists"}`))
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		args  []string
		relTo int
	}{
		{name: "path", args: []string{"/stale"}},
		{name: "port", relTo: 8080},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
			saveState(state{Claims: []stateClaim{{
				ClaimID: "stale", Gateway: client.Gateway(), Paths: []string{"/stale"}, To: 8080,
			}}})
			var out, stderr bytes.Buffer
			if err := runRelease(newConsole(&out, &stderr), context.Background(), client, test.args, test.relTo); err != nil {
				t.Fatalf("runRelease() = %v", err)
			}
			if len(loadState().Claims) != 0 {
				t.Fatalf("stale state remains: %#v", loadState().Claims)
			}
			if !strings.Contains(out.String(), "Already released: /stale") || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", out.String(), stderr.String())
			}
		})
	}
}

func TestReleaseStaleByPortSummarySeparatesAlreadyAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"claim_not_found","detail":"claim no longer exists"}`))
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	saveState(state{Claims: []stateClaim{{
		ClaimID: "stale", Gateway: client.Gateway(), Paths: []string{"/stale"}, To: 8080,
	}}})
	var out, stderr bytes.Buffer
	if err = runRelease(newConsoleOutput(&out, &stderr, "json"), context.Background(), client, nil, 8080); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("JSON output = %q", out.String())
	}
	var summary map[string]any
	if err = json.Unmarshal([]byte(lines[1]), &summary); err != nil {
		t.Fatal(err)
	}
	if summary["released"] != float64(0) || summary["already_absent"] != float64(1) || summary["failed"] != float64(0) {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestReleaseDoesNotLoseLiveReconnectingSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"claim_not_found","detail":"claim no longer exists"}`))
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		args  []string
		relTo int
	}{
		{name: "path", args: []string{"/reconnecting"}},
		{name: "port", relTo: 8080},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
			saveState(state{Claims: []stateClaim{{
				ClaimID: "old", Gateway: client.Gateway(), Paths: []string{"/reconnecting"},
				To: 8080, PID: os.Getpid(),
			}}})
			err := runRelease(newConsole(&bytes.Buffer{}, &bytes.Buffer{}), context.Background(), client, test.args, test.relTo)
			if test.relTo > 0 {
				var partial *partialReleaseError
				if !errors.As(err, &partial) || len(partial.Failures) != 1 ||
					partial.Failures[0].Code != "release_pending" {
					t.Fatalf("runRelease() error = %#v", err)
				}
			} else {
				var pending *backgroundReleasePendingError
				if !errors.As(err, &pending) {
					t.Fatalf("runRelease() error = %#v", err)
				}
			}
			if len(loadState().Claims) != 1 {
				t.Fatalf("live session state was removed: %#v", loadState().Claims)
			}
		})
	}
}

func TestReleaseRemoteLookupRaceIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"claims": []map[string]any{{
				"claim_id": "gone", "owner": "dev", "paths": []string{"/gone"}, "started_at": now,
			}}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"claim_not_found","detail":"claim no longer exists"}`))
	}))
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	var out, stderr bytes.Buffer
	if err = runRelease(newConsole(&out, &stderr), context.Background(), client, []string{"/gone"}, 0); err != nil {
		t.Fatalf("runRelease() = %v", err)
	}
	if !strings.Contains(out.String(), "No active claim: /gone") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestReleasePathExplainsDisabledClaimList(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	err = runRelease(newConsole(&bytes.Buffer{}, &bytes.Buffer{}), context.Background(), client, []string{"/unknown"}, 0)
	var unavailable *claimListUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("runRelease() error = %v", err)
	}
	var stderr bytes.Buffer
	PrintError(&stderr, err)
	for _, want := range []string{"claim_list_unavailable", "release --to PORT"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("printed error %q does not mention %q", stderr.String(), want)
		}
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

// ~/.tunlease.yaml is read when a command needs a gateway, so a broken file has to
// fail that command by name before anything is dialed, and a missing file has to
// leave the command asking for a gateway instead of failing.
func TestCommandsReportConfigFileProblemsBeforeConnecting(t *testing.T) {
	for _, tt := range []struct {
		name      string
		yaml      string
		writeYAML bool
		wantErr   string
	}{
		{name: "no config file", wantErr: "gateway required"},
		{name: "unknown field", yaml: "gatewy: callbacks.example.com\n", writeYAML: true, wantErr: "field gatewy not found"},
		{name: "invalid type", yaml: "insecure: sometimes\n", writeYAML: true},
		{name: "invalid scheme", yaml: "default_scheme: ftp\n", writeYAML: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TUNLEASE_GATEWAY", "")
			path := filepath.Join(home, ".tunlease.yaml")
			if tt.writeYAML {
				if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			command := NewCommandWithVersion("dev", "unknown")
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			command.SetArgs([]string{"list"})
			err := command.Execute()
			if err == nil {
				t.Fatal("a command with no usable gateway succeeded")
			}
			if tt.writeYAML && !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not identify %s", err, path)
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// A valid config file supplies the gateway a command would otherwise demand.
func TestConfigFileSuppliesTheGateway(t *testing.T) {
	var authorized bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized = r.Header.Get("Authorization") == "Bearer secret"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"claims":[]}`))
	}))
	defer server.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TUNLEASE_GATEWAY", "")
	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	content := "gateway: " + strings.TrimPrefix(server.URL, "http://") +
		"\ntoken: secret\ninsecure: true\ndefault_scheme: http\n"
	if err := os.WriteFile(filepath.Join(home, ".tunlease.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	command := NewCommandWithVersion("dev", "unknown")
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"list"})
	if err := command.Execute(); err != nil {
		t.Fatalf("list with a configured gateway = %v", err)
	}
	if !authorized {
		t.Error("the configured token did not reach the gateway")
	}
}
