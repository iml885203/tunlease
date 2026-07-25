package gatewayconfig

import "testing"

func TestDefaultsAndValidation(t *testing.T) {
	config := Config{FailOpenURL: "http://app.default.svc"}
	config.Defaults()
	if config.Listen != ":8300" || config.MaxClaims != 64 {
		t.Fatalf("defaults = %+v", config)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOriginIsRequired(t *testing.T) {
	for _, origin := range []string{"", "redis://app", "not a url"} {
		config := Config{MaxClaims: 64, FailOpenURL: origin}
		if err := config.Validate(); err == nil {
			t.Fatalf("FailOpenURL %q passed validation", origin)
		}
	}
}

func TestTokenValidation(t *testing.T) {
	config := Config{
		MaxClaims:   64,
		FailOpenURL: "http://app",
		Tokens:      []Token{{Owner: "alice"}},
	}
	if err := config.Validate(); err == nil {
		t.Fatal("token without value passed validation")
	}
	config.Tokens = []Token{{Owner: "alice", Token: "same"}, {Owner: "bob", Token: "same"}}
	if err := config.Validate(); err == nil {
		t.Fatal("duplicate token passed validation")
	}
}

func TestWhitelistValidation(t *testing.T) {
	for _, prefix := range []string{"webhooks/", "/webhooks", "/webhooks/*"} {
		config := Config{MaxClaims: 64, FailOpenURL: "http://app", Whitelist: []string{prefix}}
		if err := config.Validate(); err == nil {
			t.Fatalf("whitelist prefix %q passed validation", prefix)
		}
	}
}
