package cliapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
	"github.com/spf13/cobra"
)

func TestConsolePlainOutputHasSemanticMarkersAndNoANSI(t *testing.T) {
	var out, stderr bytes.Buffer
	ui := newConsole(&out, &stderr)

	ui.success("Connected: %s → localhost:8080", "/callback")
	ui.noticeOut("Waiting for requests… (Ctrl+C to release)")
	ui.activity("GET", "/callback", 200, "42ms")
	ui.activity("POST", "/callback", 502, "3ms")
	ui.claimHeader()
	ui.claimRow("/callback", "→ localhost:8080", "connected", "12:34:56", "  (you)", true, false)
	ui.warning("WARNING: Local service unavailable")
	ui.failure("claim failed")

	gotOut := out.String()
	for _, want := range []string{"Connected: /callback → localhost:8080", "Waiting for requests…", "→ GET", "200", "→ POST", "502", "PATH", "FORWARDS TO / OWNER", "STARTED", "localhost:8080", "(you)"} {
		if !strings.Contains(gotOut, want) {
			t.Errorf("stdout %q does not contain %q", gotOut, want)
		}
	}
	if strings.Contains(gotOut+stderr.String(), "\x1b[") {
		t.Fatalf("non-TTY output contains ANSI: %q %q", gotOut, stderr.String())
	}
	if !strings.Contains(gotOut, "→ GET /callback  200  42ms\n") {
		t.Fatalf("plain activity format changed: %q", gotOut)
	}
	wantRow := fmt.Sprintf("%-40s %-24s %-16s %s%s\n", "/callback", "→ localhost:8080", "connected", "12:34:56", "  (you)")
	if !strings.Contains(gotOut, wantRow) {
		t.Fatalf("plain list row format changed: %q, want %q", gotOut, wantRow)
	}
	if !strings.Contains(stderr.String(), "WARNING: Local service unavailable") ||
		!strings.Contains(stderr.String(), "claim failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestConsoleColorCanBeRenderedWithoutChangingText(t *testing.T) {
	var out, stderr bytes.Buffer
	ui := newConsole(&out, &stderr)
	ui.colorOut = true
	ui.colorErr = true
	ui.success("Connected: /callback → localhost:8080")
	ui.noticeOut("Waiting for requests… (Ctrl+C to release)")
	ui.activity("GET", "/callback", 200, "1ms")
	ui.activity("GET", "/callback", 302, "1ms")
	ui.activity("GET", "/callback", 404, "1ms")
	ui.activity("GET", "/callback", 502, "1ms")
	ui.claimRow("/callback", "owner=dev", "expires in 1m", "12:34:56", "", false, true)
	ui.warning("WARNING: local service unavailable")
	ui.failure("claim failed")

	got := out.String()
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("colored output has no ANSI: %q", got)
	}
	if !strings.Contains(got, "GET") || !strings.Contains(got, "404") {
		t.Fatalf("colored output lost content: %q", got)
	}
	for name, sequence := range map[string]string{
		"success/2xx": "\x1b[32m",
		"status/3xx":  "\x1b[36m",
		"4xx":         "\x1b[33;1m",
		"5xx":         "\x1b[31;1m",
	} {
		if !strings.Contains(got, sequence) {
			t.Errorf("%s color missing from %q", name, got)
		}
	}
	for name, sequence := range map[string]string{
		"usage hint":    "\x1b[36mWaiting for requests…",
		"finite expiry": "\x1b[36mexpires in 1m",
	} {
		if !strings.Contains(got, sequence) {
			t.Errorf("%s is not cyan in %q", name, got)
		}
	}
	if !strings.Contains(stderr.String(), "\x1b[") ||
		!strings.Contains(stderr.String(), "WARNING:") ||
		!strings.Contains(stderr.String(), "claim failed") {
		t.Fatalf("colored stderr = %q", stderr.String())
	}
}

func TestPrintErrorKeepsOnePlainSeverityMarker(t *testing.T) {
	var stderr bytes.Buffer
	PrintError(&stderr, fmt.Errorf("claim failed"))
	if got, want := stderr.String(), "Error: claim failed\n"; got != want {
		t.Fatalf("PrintError() = %q, want %q", got, want)
	}
}

// A gateway error reaches the user as printed output, so the recovery action it
// should suggest is asserted on what PrintError writes.
func TestPrintErrorSuggestsARecoveryAction(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    *tunnelclient.APIError
		action string
	}{
		{
			name:   "path outside the allowlist",
			err:    &tunnelclient.APIError{Status: 403, Code: "path_not_allowed", Detail: "path is outside the allowlist"},
			action: "Ask the gateway operator for an allowed path.",
		},
		{
			name:   "client too old",
			err:    &tunnelclient.APIError{Code: "client_upgrade_required", Detail: "incompatible"},
			action: "Upgrade tul and retry.",
		},
		{
			name:   "gateway too old",
			err:    &tunnelclient.APIError{Code: "gateway_upgrade_required", Detail: "incompatible"},
			action: "Ask the gateway operator to upgrade Tunlease.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			PrintError(&stderr, tt.err)
			got := stderr.String()
			for _, want := range []string{"Error: " + tt.err.Code, tt.err.Detail, tt.action} {
				if !strings.Contains(got, want) {
					t.Errorf("PrintError() = %q, missing %q", got, want)
				}
			}
		})
	}
}

func TestJSONPartialReleaseErrorIncludesSummary(t *testing.T) {
	command := &cobra.Command{}
	command.Flags().String("output", "json", "")
	var stderr bytes.Buffer
	PrintCommandError(&stderr, command, []string{"--output", "json"}, &partialReleaseError{
		Released: 1,
		Failures: []releaseFailure{{Paths: []string{"/b"}, Code: "internal_error", Message: "try again"}},
	})
	var event map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &event); err != nil {
		t.Fatalf("JSON error = %q: %v", stderr.String(), err)
	}
	if event["code"] != "partial_release" || event["released"] != float64(1) || event["failed"] != float64(1) {
		t.Fatalf("JSON error = %#v", event)
	}
	failures, ok := event["failures"].([]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("JSON failures = %#v", event["failures"])
	}
	failure, ok := failures[0].(map[string]any)
	if !ok || failure["code"] != "internal_error" {
		t.Fatalf("JSON failure = %#v", failures[0])
	}
}

// The environment decides whether a console emits ANSI. Output here is a buffer
// rather than a TTY, so color appears only when FORCE_COLOR asks for it and
// never when NO_COLOR or a dumb terminal forbids it.
func TestEnvironmentDecidesWhetherOutputCarriesANSI(t *testing.T) {
	for _, tt := range []struct {
		name       string
		noColor    string
		term       string
		forceColor string
		wantANSI   bool
	}{
		{name: "non-TTY output stays plain", term: "xterm-256color"},
		{name: "FORCE_COLOR colors non-TTY output", term: "xterm-256color", forceColor: "1", wantANSI: true},
		{name: "NO_COLOR overrides FORCE_COLOR", noColor: "1", term: "xterm-256color", forceColor: "1"},
		{name: "NO_COLOR alone stays plain", noColor: "1", term: "xterm-256color"},
		{name: "dumb terminal stays plain", term: "dumb", forceColor: "1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			t.Setenv("TERM", tt.term)
			t.Setenv("FORCE_COLOR", tt.forceColor)

			var out, stderr bytes.Buffer
			newConsole(&out, &stderr).success("Connected: %s", "/callback")

			got := out.String()
			if !strings.Contains(got, "Connected: /callback") {
				t.Fatalf("output lost its text: %q", got)
			}
			if hasANSI := strings.Contains(got, "\x1b["); hasANSI != tt.wantANSI {
				t.Errorf("output carries ANSI = %v, want %v (%q)", hasANSI, tt.wantANSI, got)
			}
		})
	}
}
