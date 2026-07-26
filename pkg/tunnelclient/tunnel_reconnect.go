package tunnelclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type tunnelUpdate struct {
	claim Claim
	err   error
	event *Event
}

func startTunnel(ctx context.Context, client *Client, paths []string, to int) (Claim, <-chan tunnelUpdate, context.CancelFunc, error) {
	server, err := tunnelURL(client.gateway)
	if err != nil {
		return Claim{}, nil, nil, err
	}
	tunnelContext, cancel := context.WithCancel(ctx)
	session, claim, err := dialTunnel(tunnelContext, client.http, server, client.token, paths, to, "")
	if err != nil {
		cancel()
		return Claim{}, nil, nil, err
	}
	reconnects := make(chan tunnelUpdate, 1)
	go reconnectLoop(tunnelContext, session, claim, client.http, server, client.token, paths, to, reconnects)
	return claim, reconnects, cancel, nil
}

func reconnectLoop(
	ctx context.Context,
	current *liveTunnel,
	claim Claim,
	httpClient *http.Client,
	server, token string,
	paths []string,
	to int,
	reconnects chan<- tunnelUpdate,
) {
	defer close(reconnects)
	for {
		select {
		case <-ctx.Done():
			_ = current.session.Close()
			return
		case event := <-current.events:
			select {
			case reconnects <- tunnelUpdate{event: &event}:
			case <-ctx.Done():
				_ = current.session.Close()
				return
			}
			continue
		case <-current.session.CloseChan():
		}
		select {
		case terminalErr := <-current.terminal:
			select {
			case reconnects <- tunnelUpdate{err: terminalErr}:
			case <-ctx.Done():
			}
			return
		default:
		}

		select {
		case reconnects <- tunnelUpdate{event: &Event{Type: EventTunnelDisconnected}}:
		case <-ctx.Done():
			return
		}
		for ctx.Err() == nil {
			if !waitRetry(ctx, time.Second) {
				return
			}
			next, nextClaim, err := dialTunnel(ctx, httpClient, server, token, paths, to, claim.ID)
			if err != nil {
				var apiErr *APIError
				if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 {
					select {
					case reconnects <- tunnelUpdate{err: err}:
					case <-ctx.Done():
					}
					return
				}
				continue
			}
			current, claim = next, nextClaim
			select {
			case reconnects <- tunnelUpdate{claim: claim}:
			case <-ctx.Done():
				_ = current.session.Close()
				return
			}
			break
		}
	}
}

func waitRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func tunnelURL(base string) (string, error) {
	endpoint, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	default:
		return "", errors.New("gateway URL scheme must be http or https")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/tunnel"
	return endpoint.String(), nil
}
