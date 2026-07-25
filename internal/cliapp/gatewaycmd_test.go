package cliapp

import (
	"os"
	"path/filepath"
	"testing"
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
