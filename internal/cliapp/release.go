package cliapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

func runRelease(ui *console, ctx context.Context, c *tunnelclient.Client, args []string, relTo int) error {
	st := loadState()
	if relTo > 0 {
		released := 0
		alreadyAbsent := 0
		failures := make([]releaseFailure, 0)
		// Iterate a snapshot because successful releases remove entries from st.
		for _, s := range append([]stateClaim(nil), st.Claims...) {
			if s.To == relTo && s.Gateway == c.Gateway() {
				if e := c.Release(ctx, s.ClaimID); e != nil {
					if claimAlreadyAbsent(e) {
						if s.PID > 0 && processAlive(s.PID) {
							failures = append(failures, releaseFailure{
								Paths: s.Paths, Code: "release_pending",
								Message: "tunnel process is still running while its claim reconnects",
							})
							continue
						}
						st.removeByID(s.ClaimID)
						saveState(st)
						printReleased(ui, s.Paths, relTo, true)
						alreadyAbsent++
						continue
					}
					code, message, _ := errorDetails(e)
					failures = append(failures, releaseFailure{Paths: s.Paths, Code: code, Message: message})
					continue
				}
				st.removeByID(s.ClaimID)
				// Persist each completed release so a later failure cannot
				// resurrect stale local state.
				saveState(st)
				printReleased(ui, s.Paths, relTo, false)
				released++
			}
		}
		if len(failures) > 0 {
			return &partialReleaseError{
				Released: released, AlreadyAbsent: alreadyAbsent, Failures: failures,
				LocalPort: relTo, Gateway: c.Gateway(),
			}
		}
		if ui.json {
			ui.event(map[string]any{
				"type": "release_summary", "released": released, "failed": 0,
				"already_absent": alreadyAbsent, "local_port": relTo, "gateway": c.Gateway(),
			})
			return nil
		}
		if released == 0 && alreadyAbsent == 0 {
			ui.info("No claims for localhost:%d.", relTo)
		}
		return nil
	}
	target, e := NormalizePath(args[0])
	if e != nil {
		return e
	}
	// Check local state first, then the gateway for claims created elsewhere.
	for _, s := range st.Claims {
		if s.Gateway == c.Gateway() && contains(s.Paths, target) {
			if e := c.Release(ctx, s.ClaimID); e != nil {
				if claimAlreadyAbsent(e) {
					if s.PID > 0 && processAlive(s.PID) {
						return &backgroundReleasePendingError{}
					}
					st.removeByID(s.ClaimID)
					saveState(st)
					printReleased(ui, []string{target}, s.To, true)
					return nil
				}
				return e
			}
			st.removeByID(s.ClaimID)
			saveState(st)
			printReleased(ui, []string{target}, s.To, false)
			return nil
		}
	}
	claims, e := c.List(ctx)
	if e != nil {
		if claimListUnavailable(e) {
			return &claimListUnavailableError{}
		}
		return e
	}
	for _, x := range claims {
		if contains(x.Paths, target) {
			if e := c.Release(ctx, x.ID); e != nil {
				if claimAlreadyAbsent(e) {
					printMissingRelease(ui, target, c.Gateway())
					return nil
				}
				return e
			}
			if ui.json {
				ui.event(map[string]any{"type": "released", "paths": []string{target}})
			} else {
				ui.success("Released: %s", target)
			}
			return nil
		}
	}
	printMissingRelease(ui, target, c.Gateway())
	return nil
}

func printMissingRelease(ui *console, target, gateway string) {
	if ui.json {
		ui.event(map[string]any{
			"type": "release_summary", "released": 0, "failed": 0,
			"paths": []string{target}, "gateway": gateway,
		})
		return
	}
	ui.info("No active claim: %s", target)
}

func claimAlreadyAbsent(err error) bool {
	var apiErr *tunnelclient.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "claim_not_found"
}

type backgroundReleasePendingError struct{}

func (e *backgroundReleasePendingError) Error() string {
	return "tunnel process is reconnecting; its release could not yet be confirmed"
}

func (e *backgroundReleasePendingError) CLIErrorCode() string {
	return "release_pending"
}

func printReleased(ui *console, paths []string, localPort int, alreadyAbsent bool) {
	if ui.json {
		event := map[string]any{"type": "released", "paths": paths, "local_port": localPort}
		if alreadyAbsent {
			event["already_absent"] = true
		}
		ui.event(event)
		return
	}
	if alreadyAbsent {
		ui.info("Already released: %s", strings.Join(paths, " "))
		return
	}
	ui.success("Released: %s", strings.Join(paths, " "))
}

type partialReleaseError struct {
	Released      int
	AlreadyAbsent int
	Failures      []releaseFailure
	LocalPort     int
	Gateway       string
}

type releaseFailure struct {
	Paths   []string `json:"paths"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
}

func (e *partialReleaseError) Error() string {
	failures := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		failures = append(failures, fmt.Sprintf("%s: %s", strings.Join(failure.Paths, " "), failure.Message))
	}
	return fmt.Sprintf(
		"partial release: released %d, already absent %d, failed %d (%s)",
		e.Released,
		e.AlreadyAbsent,
		len(e.Failures),
		strings.Join(failures, "; "),
	)
}

func (e *partialReleaseError) CLIErrorCode() string {
	return "partial_release"
}

func (e *partialReleaseError) CLIErrorFields() map[string]any {
	return map[string]any{
		"released":       e.Released,
		"already_absent": e.AlreadyAbsent,
		"failed":         len(e.Failures),
		"failures":       e.Failures,
		"local_port":     e.LocalPort,
		"gateway":        e.Gateway,
	}
}

func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
