package gatewayconfig

import (
	"errors"
	"net"
	"os"
	"strings"
	"time"
)

type Token struct {
	Owner string `yaml:"owner"`
	Token string `yaml:"token"`
}
type Config struct {
	Listen           string   `yaml:"listen"`
	AdvertiseHost    string   `yaml:"advertise_host"`
	MaxClaims        int      `yaml:"max_claims"`
	TTLSeconds       int      `yaml:"ttl_seconds"`
	HeartbeatSeconds int      `yaml:"heartbeat_seconds"`
	Whitelist        []string `yaml:"whitelist"`
	Tokens           []Token  `yaml:"tokens"`
	Registry         string   `yaml:"registry"`
	RedisURL         string   `yaml:"redis_url"`
	RedisPrefix      string   `yaml:"redis_prefix"`
	// ControlPrefix is the URL path prefix under which the control plane (claim
	// API, tunnel WebSocket, healthz) is served. It keeps the control plane out
	// of the way of third-party paths on the SAME domain — e.g. with the default
	// "/_tunlease", claims live at /_tunlease/api/v1/claims and everything else
	// is treated as third-party traffic. Set it to "" to serve the control plane
	// at the root (different-domain deployments, where the gateway has its own
	// host and no third-party paths to avoid).
	ControlPrefix string `yaml:"control_prefix"`
	// FailOpenURL, if set, is where third-party requests that match no active
	// claim are proxied (the original application). When empty, unmatched
	// requests get a 404.
	FailOpenURL string `yaml:"fail_open_url"`
}

func (c *Config) Defaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.TTLSeconds == 0 {
		c.TTLSeconds = 300
	}
	if c.HeartbeatSeconds == 0 {
		c.HeartbeatSeconds = 30
	}
	if c.MaxClaims == 0 {
		c.MaxClaims = 64
	}
	if c.Registry == "" {
		c.Registry = "memory"
	}
	if c.RedisPrefix == "" {
		c.RedisPrefix = "tunlease"
	}
	// Default to same-domain deployment: keep the control plane under a distinct
	// prefix so it doesn't collide with the application's own paths. Set
	// control_prefix to "/" for a different-domain deployment (control plane at
	// the root of a dedicated host).
	if c.ControlPrefix == "" {
		c.ControlPrefix = "/_tunlease"
	}
	// Normalize: strip trailing slash; treat "/" as root (no prefix).
	c.ControlPrefix = strings.TrimRight(c.ControlPrefix, "/")
}
func (c Config) Validate() error {
	if c.MaxClaims < 1 {
		return errors.New("max_claims must be greater than 0")
	}
	for _, t := range c.Tokens {
		if t.Owner == "" || t.Token == "" {
			return errors.New("each token requires owner and token")
		}
	}
	if c.Registry != "memory" && c.Registry != "redis" {
		return errors.New("registry must be memory or redis")
	}
	if c.Registry == "redis" && c.RedisURL == "" {
		return errors.New("redis_url is required for redis registry")
	}
	if c.ControlPrefix != "" && !strings.HasPrefix(c.ControlPrefix, "/") {
		return errors.New("control_prefix must start with /")
	}
	return nil
}
func ResolveAdvertiseHost(configured string, getenv func(string) string, detect func() (string, error)) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if v := getenv("POD_IP"); v != "" {
		return v, nil
	}
	if detect == nil {
		detect = OutboundIP
	}
	return detect()
}
func OutboundIP() (string, error) {
	c, e := net.DialTimeout("udp", "8.8.8.8:80", time.Second)
	if e != nil {
		return "", e
	}
	defer func() { _ = c.Close() }()
	a, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", errors.New("could not detect outbound IP")
	}
	return a.IP.String(), nil
}
