package cliapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type config struct {
	Gateway       string `yaml:"gateway"`
	Token         string `yaml:"token"`
	Insecure      bool   `yaml:"insecure"`
	DefaultScheme string `yaml:"default_scheme"`
}

func loadConfig() (config, error) {
	var c config
	h, e := os.UserHomeDir()
	if e != nil {
		return c, nil
	}
	path := filepath.Join(h, ".tunlease.yaml")
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return c, nil
	}
	if e != nil {
		return c, fmt.Errorf("%s: %w", path, e)
	}
	if strings.TrimSpace(string(b)) == "" {
		return c, nil
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(b)))
	decoder.KnownFields(true)
	if e := decoder.Decode(&c); e != nil {
		return config{}, fmt.Errorf("%s: %w", path, e)
	}
	if c.DefaultScheme != "" && c.DefaultScheme != "http" && c.DefaultScheme != "https" {
		return config{}, fmt.Errorf("%s: default_scheme must be http or https", path)
	}
	return c, nil
}
