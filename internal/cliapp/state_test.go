package cliapp

import "testing"

func TestRemoveSessionMatchesIdentityNotClaimID(t *testing.T) {
	s := state{Claims: []stateClaim{
		{ClaimID: "expired", Gateway: "https://gateway", Paths: []string{"/b/*", "/a/*"}, To: 8080},
		{ClaimID: "other-path", Gateway: "https://gateway", Paths: []string{"/other/*"}, To: 8080},
		{ClaimID: "other-port", Gateway: "https://gateway", Paths: []string{"/a/*", "/b/*"}, To: 9090},
		{ClaimID: "other-gateway", Gateway: "https://other", Paths: []string{"/a/*", "/b/*"}, To: 8080},
	}}

	s.removeSession("https://gateway", 8080, []string{"/a/*", "/b/*"})
	if len(s.Claims) != 3 {
		t.Fatalf("claims = %#v", s.Claims)
	}
	for _, claim := range s.Claims {
		if claim.ClaimID == "expired" {
			t.Fatal("matching stale claim was not removed")
		}
	}
}

func TestRemoveSessionRemovesReclaimedIDs(t *testing.T) {
	s := state{Claims: []stateClaim{
		{ClaimID: "old", Gateway: "https://gateway", Paths: []string{"/callback/*"}, To: 8080},
		{ClaimID: "new", Gateway: "https://gateway", Paths: []string{"/callback/*"}, To: 8080},
	}}
	s.removeSession("https://gateway", 8080, []string{"/callback/*"})
	if len(s.Claims) != 0 {
		t.Fatalf("claims = %#v", s.Claims)
	}
}
