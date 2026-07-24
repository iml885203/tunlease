package gatewayconfig

import (
	"errors"
	"testing"
)

func TestResolveAdvertiseHost(t *testing.T) {
	detect := func() (string, error) { return "10.0.0.3", nil }
	tests := []struct{ name, cfg, pod, want string }{{"configured", "host.example", "10.0.0.2", "host.example"}, {"pod", "", "10.0.0.2", "10.0.0.2"}, {"detected", "", "", "10.0.0.3"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, e := ResolveAdvertiseHost(tt.cfg, func(string) string { return tt.pod }, detect)
			if e != nil || got != tt.want {
				t.Fatalf("got %q, %v", got, e)
			}
		})
	}
	_, e := ResolveAdvertiseHost("", func(string) string { return "" }, func() (string, error) { return "", errors.New("no route") })
	if e == nil {
		t.Fatal("expected detection error")
	}
}

func TestValidateAllowsNoClientTokens(t *testing.T) {
	cfg := Config{PortPool: PortPool{Start: 4000, End: 4001}, Registry: "memory"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
