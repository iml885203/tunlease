package cliapp

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestConsolePlainOutputHasSemanticMarkersAndNoANSI(t *testing.T) {
	var out, stderr bytes.Buffer
	ui := newConsole(&out, &stderr)

	ui.success("claimed %s", "/callback")
	ui.status("tunnel connected  (Ctrl+C to release)")
	ui.activity("GET", "/callback", 200, "42ms")
	ui.activity("POST", "/callback", 502, "3ms")
	ui.claimRow("/callback", "→ localhost:8080", "connected", "12:34:56", "  (you)", true, false)
	ui.warning("WARNING: Local service unavailable")
	ui.failure("claim failed")

	gotOut := out.String()
	for _, want := range []string{"claimed /callback", "tunnel connected", "→ GET", "200", "→ POST", "502", "localhost:8080", "(you)"} {
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
	ui.success("claimed /callback")
	ui.status("tunnel connected  (Ctrl+C to release)")
	ui.noticeOut("WARNING: real 3rd-party traffic now flows to localhost:8080")
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
		"safety notice": "\x1b[36mWARNING: real 3rd-party traffic",
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

func TestNoColorDisablesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "xterm-256color")
	if colorPermitted() {
		t.Fatal("NO_COLOR did not disable color")
	}
}

func TestDumbTerminalDisablesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if colorPermitted() {
		t.Fatal("TERM=dumb did not disable color")
	}
}

func TestNormalTerminalPermitsColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !colorPermitted() {
		t.Fatal("normal terminal environment disabled color")
	}
}
