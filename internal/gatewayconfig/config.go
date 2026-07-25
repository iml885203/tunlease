package gatewayconfig

import (
	"errors"
	"net/url"
	"strings"
)

type Token struct {
	Owner string `yaml:"owner"`
	Token string `yaml:"token"`
}

// Config intentionally exposes only the supported v1 deployment: one
// whole-host gateway, an in-memory active-tunnel registry, and one origin.
type Config struct {
	Listen      string   `yaml:"listen"`
	MaxClaims   int      `yaml:"max_claims"`
	Whitelist   []string `yaml:"whitelist"`
	Tokens      []Token  `yaml:"tokens"`
	FailOpenURL string   `yaml:"fail_open_url"`
}

func (c *Config) Defaults() {
	if c.Listen == "" {
		c.Listen = ":8300"
	}
	if c.MaxClaims == 0 {
		c.MaxClaims = 64
	}
}

func (c Config) Validate() error {
	if c.MaxClaims < 1 {
		return errors.New("max_claims must be greater than 0")
	}
	seenTokens := make(map[string]struct{}, len(c.Tokens))
	for _, token := range c.Tokens {
		if token.Owner == "" || token.Token == "" {
			return errors.New("each token requires owner and token")
		}
		if _, exists := seenTokens[token.Token]; exists {
			return errors.New("token values must be unique")
		}
		seenTokens[token.Token] = struct{}{}
	}
	for _, prefix := range c.Whitelist {
		if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") || strings.Contains(prefix, "*") {
			return errors.New("each whitelist prefix must start and end with / and contain no wildcard")
		}
	}
	if c.FailOpenURL == "" {
		return errors.New("fail_open_url is required")
	}
	origin, err := url.Parse(c.FailOpenURL)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" {
		return errors.New("fail_open_url must be an http(s) URL")
	}
	return nil
}
