package cliapp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// runDetach re-execs this binary as a detached background daemon that holds the
// claim, then waits until the daemon has recorded the claim in the state file
// and returns its id. It makes `tunle claim --detach` non-blocking, which is
// what an agent needs: the call returns once the tunnel is up, and the claim is
// torn down later with `tunle release`.
func runDetach(paths []string, to int, gateway, token string, insecure bool, scheme string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := daemonLogPath(to)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	// Rebuild the argv for the daemon: the same claim, with --_daemon so the
	// child runs the foreground loop, and the connection options passed through
	// explicitly so the child doesn't depend on inherited flags.
	args := append([]string{"claim", "--_daemon", "--to", itoa(to)}, paths...)
	if gateway != "" {
		args = append(args, "--gateway", gateway)
	}
	if token != "" {
		args = append(args, "--token", token)
	}
	if insecure {
		args = append(args, "--insecure")
	}
	cmd := exec.Command(self, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	if scheme != "" {
		cmd.Env = append(cmd.Env, "TUNLEASE_DEFAULT_SCHEME="+scheme)
	}
	// Start independently so the daemon survives the parent shell / agent turn.
	configureDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	// Let the daemon run independently.
	_ = cmd.Process.Release()

	// Wait for the daemon to record the claim (or fail) — up to ~15s.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return fmt.Errorf("background claim failed to start; see %s", logPath)
		}
		if c, ok := findDaemonClaim(gateway, to, paths); ok {
			fmt.Printf("claimed %s in the background (claim %s, pid %d)\n", strings.Join(paths, " "), shortID(c.ClaimID), pid)
			fmt.Printf("logs: %s\n", logPath)
			fmt.Printf("release with: tunle release --to %d\n", to)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for the background claim; see %s", logPath)
}

// findDaemonClaim looks up the state entry the daemon writes once its tunnel is
// connected, matching gateway/port/paths.
func findDaemonClaim(gateway string, to int, paths []string) (stateClaim, bool) {
	want := append([]string(nil), paths...)
	slices.Sort(want)
	for _, c := range loadState().Claims {
		if c.To != to || c.PID == 0 {
			continue
		}
		got := append([]string(nil), c.Paths...)
		slices.Sort(got)
		if slices.Equal(got, want) {
			return c, true
		}
	}
	return stateClaim{}, false
}

func daemonLogPath(to int) string {
	base := filepath.Dir(statePath())
	if base == "" || base == "." {
		base = os.TempDir()
	}
	return filepath.Join(base, fmt.Sprintf("claim-%d.log", to))
}

// stopDaemon kills the background daemon holding a claim, if one is recorded.
func stopDaemon(c stateClaim) {
	if c.PID > 0 {
		terminateProcess(c.PID)
	}
}

func itoa(v int) string { return fmt.Sprintf("%d", v) }
