package cliapp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

func TestRunDoctorReady(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_tunlease/api/v1/claims" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"claims":[]}`)
	}))
	defer gateway.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
		}
	}()

	client, err := tunnelclient.New(tunnelclient.Config{Gateway: gateway.URL, HTTPClient: gateway.Client()})
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := runDoctor(context.Background(), client, "/webhooks/provider/*", port); err != nil {
		t.Fatal(err)
	}
}

func TestRunDoctorReportsProblems(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized","detail":"invalid token"}`)
	}))
	defer gateway.Close()

	client, err := tunnelclient.New(tunnelclient.Config{Gateway: gateway.URL, HTTPClient: gateway.Client()})
	if err != nil {
		t.Fatal(err)
	}
	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := closedListener.Addr().(*net.TCPAddr).Port
	if err := closedListener.Close(); err != nil {
		t.Fatal(err)
	}
	err = runDoctor(context.Background(), client, "/webhooks/provider/*", closedPort)
	if err == nil || err.Error() != "doctor found 2 problem(s)" {
		t.Fatalf("error = %v", err)
	}
}

func TestClaimedBy(t *testing.T) {
	claims := []tunnelclient.Claim{{Owner: "alice", Paths: []string{"/webhooks/provider/*"}}}
	for _, path := range []string{"/webhooks/provider/*", "/webhooks/provider/event/*"} {
		if owner, ok := claimedBy(claims, path); !ok || owner != "alice" {
			t.Fatalf("claimedBy(%q) = %q, %v", path, owner, ok)
		}
	}
	if owner, ok := claimedBy(claims, "/other/*"); ok {
		t.Fatalf("claimedBy(other) = %q, true", owner)
	}
}

func TestDoctorCommandRejectsInvalidPath(t *testing.T) {
	command := NewCommand()
	command.SetArgs([]string{"doctor", "relative", "--gateway", "example.test"})
	err := command.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got != `"relative": path must start with /` {
		t.Fatalf("error = %q", got)
	}
}

func Example_pathsOverlap() {
	fmt.Println(pathsOverlap("/webhooks/*", "/webhooks/stripe/*"))
	fmt.Println(pathsOverlap("/webhooks/stripe/*", "/other/*"))
	// Output:
	// true
	// false
}
