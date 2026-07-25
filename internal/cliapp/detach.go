package cliapp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// runDetach re-execs this binary as a detached background daemon that holds the
// claim, then waits until the daemon has recorded the claim in the state file
// and returns its id. It makes `tul claim --detach` non-blocking, which is
// what an agent needs: the call returns once the tunnel is up, and the claim is
// torn down later with `tul release`.
func runDetach(ui *console, paths []string, to int, gatewayArg, stateGateway, token string, insecure bool, scheme string) error {
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
	if ui.json {
		args = append(args, "--output", "json")
	}
	if gatewayArg != "" {
		args = append(args, "--gateway", gatewayArg)
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
		if c, ok := findDaemonClaim(stateGateway, to, paths, pid); ok {
			if ui.json {
				event := connectionEvent("connected", paths, to, c.ExpiresAt)
				event["background"] = true
				event["log_path"] = logPath
				event["release_command"] = fmt.Sprintf("tul release --to %d", to)
				ui.event(event)
			} else {
				ui.success("%s", connectionMessage("Connected in background", paths, to, c.ExpiresAt))
				ui.info("Logs: %s", logPath)
				ui.info("Stop with: tul release --to %d", to)
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for the background claim; see %s", logPath)
}

// findDaemonClaim looks up the state entry the daemon writes once its tunnel is
// connected, matching gateway/port/paths.
func findDaemonClaim(gateway string, to int, paths []string, pid int) (stateClaim, bool) {
	want := append([]string(nil), paths...)
	slices.Sort(want)
	for _, c := range loadState().Claims {
		if c.Gateway != gateway || c.To != to || c.PID != pid {
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

func itoa(v int) string { return fmt.Sprintf("%d", v) }
