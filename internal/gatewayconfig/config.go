package gatewayconfig

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type Token struct {
	Owner string `yaml:"owner"`
	Token string `yaml:"token"`
}

// Config intentionally exposes only the supported v1 deployment: one
// whole-host gateway, an in-memory active-tunnel registry, and one origin.
type Config struct {
	Listen                string        `yaml:"listen"`
	TunnelIdleTimeout     time.Duration `yaml:"tunnel_idle_timeout"`
	MaxClaims             int           `yaml:"max_claims"`
	MaxClaimsPerOwner     int           `yaml:"max_claims_per_owner"`
	MaxClaimDuration      time.Duration `yaml:"max_claim_duration"`
	MinClaimPathSegments  int           `yaml:"min_claim_path_segments"`
	DynamicClientIdentity bool          `yaml:"dynamic_client_identity"`
	Whitelist             []string      `yaml:"whitelist"`
	Tokens                []Token       `yaml:"tokens"`
	FailOpenURL           string        `yaml:"fail_open_url"`
	UnclaimedStatus       int           `yaml:"unclaimed_status"`
	DisableClaimList      bool          `yaml:"disable_claim_list"`
}

func (c *Config) Defaults() {
	if c.Listen == "" {
		c.Listen = ":8300"
	}
	if c.MaxClaims == 0 {
		c.MaxClaims = 64
	}
	if c.TunnelIdleTimeout == 0 {
		c.TunnelIdleTimeout = 4 * time.Hour
	}
}

func (c Config) Validate() error {
	if c.TunnelIdleTimeout < 0 {
		return errors.New("tunnel_idle_timeout must not be negative")
	}
	if c.MaxClaims < 1 {
		return errors.New("max_claims must be greater than 0")
	}
	if c.MaxClaimsPerOwner < 0 {
		return errors.New("max_claims_per_owner must not be negative")
	}
	if c.MaxClaimDuration < 0 {
		return errors.New("max_claim_duration must not be negative")
	}
	if c.MinClaimPathSegments < 0 {
		return errors.New("min_claim_path_segments must not be negative")
	}
	if c.DynamicClientIdentity && len(c.Tokens) > 0 {
		return errors.New("dynamic_client_identity and tokens are mutually exclusive")
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
