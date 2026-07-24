package cliapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const defaultReleaseURL = "https://tunlease.example.com/install"

func newUpdateCommand(current string) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use: "update", Short: "Update Tunlease from the release page", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if baseURL == "" {
				baseURL = os.Getenv("TUNLEASE_BASE_URL")
			}
			if baseURL == "" {
				baseURL = defaultReleaseURL
			}
			path, err := os.Executable()
			if err != nil {
				return err
			}
			path, _ = filepath.EvalSymlinks(path)
			name := fmt.Sprintf("tunle-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			data, err := download(ctx, strings.TrimRight(baseURL, "/")+"/"+name)
			if err != nil {
				return err
			}
			checksum, err := download(ctx, strings.TrimRight(baseURL, "/")+"/"+name+".sha256")
			if err != nil {
				return err
			}
			want := strings.Fields(string(checksum))
			if len(want) == 0 {
				return errors.New("empty checksum")
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != want[0] {
				return errors.New("download checksum mismatch")
			}
			tmp := path + ".new"
			if err := os.WriteFile(tmp, data, 0o755); err != nil {
				return err
			}
			defer func() { _ = os.Remove(tmp) }()
			if err = os.Rename(path, path+".prev"); err != nil {
				return fmt.Errorf("backup current binary: %w", err)
			}
			if err = os.Rename(tmp, path); err != nil {
				_ = os.Rename(path+".prev", path)
				return fmt.Errorf("install update: %w", err)
			}
			fmt.Printf("Updated tunle at %s (previous %s binary: %s.prev)\n", path, current, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", "", "release base URL (env TUNLEASE_BASE_URL)")
	return cmd
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 100<<20))
}
