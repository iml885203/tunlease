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
	Listen           string   `yaml:"listen"`
	MaxClaims        int      `yaml:"max_claims"`
	Whitelist        []string `yaml:"whitelist"`
	Tokens           []Token  `yaml:"tokens"`
	FailOpenURL      string   `yaml:"fail_open_url"`
	UnclaimedStatus  int      `yaml:"unclaimed_status"`
	DisableClaimList bool     `yaml:"disable_claim_list"`
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
	if c.FailOpenURL != "" && c.UnclaimedStatus != 0 {
		return errors.New("fail_open_url and unclaimed_status are mutually exclusive")
	}
	if c.FailOpenURL == "" && c.UnclaimedStatus == 0 {
		return errors.New("fail_open_url or unclaimed_status is required")
	}
	if c.UnclaimedStatus != 0 {
		if c.UnclaimedStatus < 400 || c.UnclaimedStatus > 599 {
			return errors.New("unclaimed_status must be between 400 and 599")
		}
		return nil
	}
	origin, err := url.Parse(c.FailOpenURL)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" {
		return errors.New("fail_open_url must be an http(s) URL")
	}
	return nil
}
