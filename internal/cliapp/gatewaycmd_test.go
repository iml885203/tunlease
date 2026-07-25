package cliapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/iml885203/tunlease/internal/gatewayconfig"
)

func TestLoadGatewayConfigRejectsRemovedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	data := []byte("fail_open_url: http://origin.test\ncontrol_prefix: /old\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGatewayConfig(path); err == nil {
		t.Fatal("removed config field was silently accepted")
	}
}

func TestBuildFailOpenStatus(t *testing.T) {
	handler, err := buildFailOpen(gatewayconfig.Config{UnclaimedStatus: http.StatusNotFound})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/demo/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}
