package gatewayconfig

import (
	"errors"
	"net"
	"os"
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
	SidecarToken     string   `yaml:"sidecar_token"`
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
