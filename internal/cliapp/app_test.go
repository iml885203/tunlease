package cliapp

import "testing"

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{"/webhooks/provider/callback/getbalance": "/webhooks/provider/callback/getbalance/*", "/webhooks/provider/callback/updatebalance": "/webhooks/provider/callback/updatebalance/*", "/x/": "/x/*", "/x/*": "/x/*"}
	for in, want := range cases {
		got, e := NormalizePath(in)
		if e != nil || got != want {
			t.Fatalf("%q => %q,%v", in, got, e)
		}
	}
	if _, e := NormalizePath("x"); e == nil {
		t.Fatal("accepted relative path")
	}
	if _, e := NormalizePath("/x/*/y"); e == nil {
		t.Fatal("accepted inner wildcard")
	}
}
