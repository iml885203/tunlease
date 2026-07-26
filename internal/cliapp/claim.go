package cliapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

func runClaim(ui *console, c *tunnelclient.Client, paths []string, to int, _ bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	session, e := c.Start(ctx, paths, to)
	if e != nil {
		return e
	}
	cl := session.Claim()
	// Record the holder for both foreground and detached sessions. Release uses
	// this only as a conservative liveness check; it never signals the PID.
	pid := os.Getpid()
	st := loadState()
	st.add(stateClaim{ClaimID: cl.ID, Gateway: c.Gateway(), Paths: cl.Paths, To: to, ExpiresAt: cl.ExpiresAt, PID: pid})
	saveState(st)
	defer cleanupSessionState(c.Gateway(), to, cl.Paths)

	if ui.json {
		ui.event(connectionEvent("connected", cl.Paths, to, cl.ExpiresAt))
	} else {
		ui.success("%s", connectionMessage("Connected", cl.Paths, to, cl.ExpiresAt))
		ui.noticeOut("Waiting for requests… (Ctrl+C to release)")
	}
	for {
		select {
		case <-ctx.Done():
			_ = session.Close()
			printStoppedTerminal(ui, cl.Paths)
			return nil
		case event, ok := <-session.Events():
			if !ok {
				if err := session.Err(); err != nil {
					return finishTerminalSession(ui, err, cl)
				}
				if ctx.Err() != nil {
					printStoppedTerminal(ui, cl.Paths)
				}
				return nil
			}
			switch event.Type {
			case tunnelclient.EventTunnelDisconnected:
				if ui.json {
					ui.event(map[string]any{"type": "disconnected", "state": "retrying"})
				} else {
					ui.warningOut("Connection lost; retrying…")
				}
			case tunnelclient.EventTunnelReconnected:
				previous := cl
				cl = event.Claim
				st := loadState()
				st.removeSession(c.Gateway(), to, previous.Paths)
				st.add(stateClaim{ClaimID: cl.ID, Gateway: c.Gateway(), Paths: cl.Paths, To: to, ExpiresAt: cl.ExpiresAt, PID: pid})
				saveState(st)
				if ui.json {
					ui.event(connectionEvent("reconnected", cl.Paths, to, cl.ExpiresAt))
				} else {
					ui.status("%s", connectionMessage("Reconnected", cl.Paths, to, cl.ExpiresAt))
				}
			case tunnelclient.EventLocalTargetError:
				if ui.json {
					ui.event(map[string]any{
						"type": "local_error", "target": fmt.Sprintf("localhost:%d", to),
						"local_port": to, "code": "local_unavailable", "message": localTargetError(event.Err),
					})
				} else {
					ui.warningOut("Could not reach localhost:%d: %s", to, localTargetError(event.Err))
				}
			case tunnelclient.EventRequestActivity:
				if ui.json {
					ui.event(map[string]any{
						"type": "request", "method": event.Method, "path": event.Path,
						"status": event.Status, "duration_ms": event.Duration.Milliseconds(),
					})
				} else {
					ui.activity(event.Method, event.Path, event.Status, formatActivityDuration(event.Duration))
				}
			}
		}
	}
}

func printStoppedTerminal(ui *console, paths []string) {
	if ui.json {
		ui.event(map[string]any{"type": "released", "paths": paths})
	} else {
		ui.info("\nReleased.")
	}
}

func finishTerminalSession(ui *console, err error, claim tunnelclient.Claim) error {
	terminal, expected := expectedTerminalReason(err)
	if !expected {
		return err
	}
	printExpectedTerminal(ui, terminal, claim)
	return nil
}

type expectedTerminal string

const (
	terminalExpired  expectedTerminal = "expired"
	terminalReleased expectedTerminal = "released"
)

func expectedTerminalReason(err error) (expectedTerminal, bool) {
	var apiErr *tunnelclient.APIError
	if !errors.As(err, &apiErr) {
		return "", false
	}
	switch apiErr.Code {
	case "claim_expired":
		return terminalExpired, true
	case "claim_released":
		return terminalReleased, true
	default:
		return "", false
	}
}

func printExpectedTerminal(ui *console, terminal expectedTerminal, claim tunnelclient.Claim) {
	switch terminal {
	case terminalExpired:
		if ui.json {
			event := map[string]any{"type": "expired", "paths": claim.Paths}
			if claim.ExpiresAt != nil {
				event["expired_at"] = claim.ExpiresAt.Format(time.RFC3339Nano)
			}
			ui.event(event)
			return
		}
		if claim.ExpiresAt != nil {
			ui.info("Claim expired at %s.", claim.ExpiresAt.Local().Format("15:04:05"))
		} else {
			ui.info("Claim expired.")
		}
	case terminalReleased:
		if ui.json {
			ui.event(map[string]any{"type": "released", "paths": claim.Paths})
		} else {
			ui.info("Released.")
		}
	}
}

func connectionEvent(eventType string, paths []string, to int, expiresAt *time.Time) map[string]any {
	event := map[string]any{
		"type": eventType, "paths": paths, "target": fmt.Sprintf("localhost:%d", to), "local_port": to,
	}
	if expiresAt != nil {
		event["expires_at"] = expiresAt.Format(time.RFC3339Nano)
	}
	return event
}

func connectionMessage(status string, paths []string, to int, expiresAt *time.Time) string {
	if expiresAt != nil {
		return fmt.Sprintf("%s until %s: %s → localhost:%d", status, expiresAt.Local().Format("15:04:05"), strings.Join(paths, " "), to)
	}
	return fmt.Sprintf("%s: %s → localhost:%d", status, strings.Join(paths, " "), to)
}

func localTargetError(err error) string {
	if err == nil {
		return "unknown error"
	}
	var syscallErr *os.SyscallError
	if errors.As(err, &syscallErr) {
		return syscallErr.Err.Error()
	}
	return err.Error()
}

func formatActivityDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1ms"
	}
	return duration.Round(time.Millisecond).String()
}

func checkLocalTarget(port int) error {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), 250*time.Millisecond)
	if err != nil {
		return err
	}
	return connection.Close()
}

func cleanupSessionState(gateway string, to int, paths []string) {
	st := loadState()
	st.removeSession(gateway, to, paths)
	saveState(st)
}
