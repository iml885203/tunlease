package cliapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

func runList(ui *console, ctx context.Context, c *tunnelclient.Client, all bool) error {
	st := loadState()
	mine := map[string]stateClaim{}
	for _, s := range st.Claims {
		mine[s.ClaimID] = s
	}
	claims, e := c.List(ctx)
	if e != nil {
		if claimListUnavailable(e) {
			return &claimListUnavailableError{}
		}
		return e
	}
	shown := 0
	jsonClaims := make([]map[string]any, 0)
	for _, x := range claims {
		s, own := mine[x.ID]
		if !all && !own {
			continue
		}
		target := "owner=" + x.Owner
		suffix := ""
		status := "connected"
		expiring := false
		if x.ExpiresAt != nil {
			remaining := time.Until(*x.ExpiresAt).Round(time.Second)
			if remaining < 0 {
				remaining = 0
			}
			status = "expires in " + remaining.String()
			expiring = true
		}
		if own {
			target = fmt.Sprintf("→ localhost:%d", s.To)
			suffix = "  (you)"
		}
		if ui.json {
			item := map[string]any{
				"paths": x.Paths, "owner": x.Owner, "started_at": x.StartedAt.Format(time.RFC3339Nano),
				"mine": own, "status": "connected",
			}
			if own {
				item["target"] = fmt.Sprintf("localhost:%d", s.To)
				item["local_port"] = s.To
			}
			if x.ExpiresAt != nil {
				item["expires_at"] = x.ExpiresAt.Format(time.RFC3339Nano)
			}
			jsonClaims = append(jsonClaims, item)
			shown++
			continue
		}
		if shown == 0 {
			ui.claimHeader()
		}
		ui.claimRow(strings.Join(x.Paths, ","), target, status, x.StartedAt.Local().Format("15:04:05"), suffix, own, expiring)
		shown++
	}
	if ui.json {
		ui.event(map[string]any{"type": "claim_list", "claims": jsonClaims})
		return nil
	}
	if shown == 0 {
		if all {
			ui.info("No active claims.")
		} else {
			ui.info("No active claims. Use --all to include others.")
		}
	}
	return nil
}

func claimListUnavailable(err error) bool {
	var apiErr *tunnelclient.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Code == ""
}

type claimListUnavailableError struct{}

func (e *claimListUnavailableError) Error() string {
	return "gateway does not expose claim lookup"
}

func (e *claimListUnavailableError) CLIErrorCode() string {
	return "claim_list_unavailable"
}
