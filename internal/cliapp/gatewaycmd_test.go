package cliapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An operator meets the config rules by writing a file and starting the gateway,
// so acceptance and rejection are asserted on the file the gateway is given.
func TestLoadGatewayConfig(t *testing.T) {
	for _, tt := range []struct {
		name     string
		yaml     string
		accepted bool
	}{
		{"an origin to fall open to", "fail_open_url: http://app.default.svc\n", true},
		{"an error status instead of an origin", "unclaimed_status: 404\n", true},
		{"durations", "fail_open_url: http://app\nmax_claim_duration: 1m\ntunnel_idle_timeout: 45s\n", true},
		{"an allowlist prefix", "fail_open_url: http://app\nwhitelist:\n  - /webhooks/\n", true},

		{"a removed field", "fail_open_url: http://origin.test\ncontrol_prefix: /old\n", false},
		{"no unclaimed target at all", "listen: \":8300\"\n", false},
		{"a non-HTTP origin", "fail_open_url: redis://app\n", false},
		{"an unparsable origin", "fail_open_url: not a url\n", false},
		{"both unclaimed targets", "fail_open_url: http://app\nunclaimed_status: 404\n", false},
		{"a success status for unclaimed requests", "unclaimed_status: 200\n", false},
		{"an out-of-range unclaimed status", "unclaimed_status: 600\n", false},
		{"a negative idle timeout", "fail_open_url: http://app\ntunnel_idle_timeout: -1s\n", false},
		{"a token without a value", "fail_open_url: http://app\ntokens:\n  - owner: alice\n", false},
		{"duplicate tokens", "fail_open_url: http://app\ntokens:\n  - owner: alice\n    token: same\n  - owner: bob\n    token: same\n", false},
		{"static tokens with a dynamic identity", "fail_open_url: http://app\ndynamic_client_identity: true\ntokens:\n  - owner: alice\n    token: secret\n", false},
		{"a relative allowlist prefix", "fail_open_url: http://app\nwhitelist:\n  - webhooks/\n", false},
		{"an allowlist prefix without a trailing slash", "fail_open_url: http://app\nwhitelist:\n  - /webhooks\n", false},
		{"a wildcard allowlist prefix", "fail_open_url: http://app\nwhitelist:\n  - /webhooks/*\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gateway.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadGatewayConfig(path)
			if accepted := err == nil; accepted != tt.accepted {
				t.Errorf("loadGatewayConfig() accepted = %v, want %v (err=%v)", accepted, tt.accepted, err)
			}
		})
	}
}

// Defaults fill in what the file omits, so an operator only configures the
// origin and still gets a working listener and claim ceiling.
func TestLoadGatewayConfigAppliesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte("fail_open_url: http://app.default.svc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := loadGatewayConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Listen != ":8300" || config.MaxClaims != 64 || config.TunnelIdleTimeout != 4*time.Hour {
		t.Errorf("defaults = %+v", config)
	}
}

// Durations are written as YAML strings, so the values the gateway runs with have
// to survive decoding rather than silently arriving as zero.
func TestLoadGatewayConfigReadsDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	data := []byte("fail_open_url: http://app\nmax_claim_duration: 1m\ntunnel_idle_timeout: 45s\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := loadGatewayConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.MaxClaimDuration != time.Minute {
		t.Errorf("max claim duration = %s, want 1m", config.MaxClaimDuration)
	}
	if config.TunnelIdleTimeout != 45*time.Second {
		t.Errorf("tunnel idle timeout = %s, want 45s", config.TunnelIdleTimeout)
	}
}

// An unclaimed request either reaches the configured origin or gets the configured
// status. Both are driven from the config file, so what the operator writes decides
// what an unclaimed caller sees.
func TestUnclaimedRequestsFollowTheConfiguredTarget(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer origin.Close()

	for _, tt := range []struct {
		name string
		yaml string
		want int
	}{
		{"a status for unclaimed requests", "unclaimed_status: 404\n", http.StatusNotFound},
		{"an origin to fall open to", "fail_open_url: " + origin.URL + "\n", http.StatusTeapot},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gateway.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := loadGatewayConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := buildFailOpen(config)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/demo/missing", nil))
			if recorder.Code != tt.want {
				t.Errorf("unclaimed request = %d, want %d", recorder.Code, tt.want)
			}
		})
	}
}
