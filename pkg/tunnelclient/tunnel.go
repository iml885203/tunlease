package tunnelclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

const (
	headerClaim    = "X-Tunlease-Claim"
	headerLocal    = "X-Tunlease-Local"
	headerOwner    = "X-Tunlease-Owner"
	headerPaths    = "X-Tunlease-Paths"
	headerReplaces = "X-Tunlease-Replaces"
	headerStarted  = "X-Tunlease-Started"
	headerExpires  = "X-Tunlease-Expires"
	streamRequest  = byte(1)
	streamRelease  = byte(2)
	streamAck      = byte(3)
	streamExpire   = byte(4)
	streamActivity = byte(5)
)

var setupTimeout = 10 * time.Second

type tunnelUpdate struct {
	claim Claim
	err   error
	event *Event
}

type liveTunnel struct {
	session  *yamux.Session
	terminal <-chan error
	events   <-chan Event
}

type activityMessage struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
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

func dialTunnel(
	ctx context.Context,
	httpClient *http.Client,
	server, token string,
	paths []string,
	to int,
	replaces string,
) (*liveTunnel, Claim, error) {
	headers := http.Header{}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	encodedPaths, _ := json.Marshal(paths)
	headers.Set(headerPaths, string(encodedPaths))
	headers.Set(headerLocal, net.JoinHostPort("localhost", strconv.Itoa(to)))
	if replaces != "" {
		headers.Set(headerReplaces, replaces)
	}

	ws, response, err := websocket.Dial(ctx, server, &websocket.DialOptions{
		HTTPClient: httpClient,
		HTTPHeader: headers,
	})
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		if response != nil && response.Body != nil {
			apiErr := APIError{Status: response.StatusCode}
			_ = json.NewDecoder(response.Body).Decode(&apiErr)
			if apiErr.Code != "" {
				return nil, Claim{}, &apiErr
			}
		}
		return nil, Claim{}, err
	}

	if response == nil {
		_ = ws.Close(websocket.StatusProtocolError, "missing handshake response")
		return nil, Claim{}, errors.New("gateway returned no tunnel metadata")
	}
	started, err := time.Parse(time.RFC3339Nano, response.Header.Get(headerStarted))
	if err != nil {
		_ = ws.Close(websocket.StatusProtocolError, "invalid tunnel metadata")
		return nil, Claim{}, errors.New("gateway returned invalid tunnel metadata")
	}
	claim := Claim{
		ID:        response.Header.Get(headerClaim),
		Owner:     response.Header.Get(headerOwner),
		Paths:     append([]string(nil), paths...),
		StartedAt: started,
	}
	if value := response.Header.Get(headerExpires); value != "" {
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, value)
		if parseErr != nil {
			_ = ws.Close(websocket.StatusProtocolError, "invalid tunnel metadata")
			return nil, Claim{}, errors.New("gateway returned invalid tunnel expiration")
		}
		claim.ExpiresAt = &expiresAt
	}
	if claim.ID == "" || claim.Owner == "" {
		_ = ws.Close(websocket.StatusProtocolError, "missing tunnel metadata")
		return nil, Claim{}, errors.New("gateway returned incomplete tunnel metadata")
	}

	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	_ = conn.SetDeadline(time.Now().Add(setupTimeout))
	session, err := yamux.Client(conn, yamuxConfig())
	if err != nil {
		_ = conn.Close()
		return nil, Claim{}, err
	}
	if err = completeClientSetup(session); err != nil {
		_ = session.Close()
		return nil, Claim{}, err
	}
	_ = conn.SetDeadline(time.Time{})
	terminal := make(chan error, 1)
	events := make(chan Event, 16)
	go acceptStreams(session, to, terminal, events)
	return &liveTunnel{session: session, terminal: terminal, events: events}, claim, nil
}

func completeClientSetup(session *yamux.Session) error {
	result := make(chan error, 1)
	go func() {
		stream, err := session.Accept()
		var message [5]byte
		if err == nil {
			_, err = io.ReadFull(stream, message[:])
		}
		if err == nil && string(message[:]) == "ready" {
			_, err = io.WriteString(stream, "ack")
		}
		var confirmation [2]byte
		if err == nil {
			_, err = io.ReadFull(stream, confirmation[:])
			if err == nil && string(confirmation[:]) != "ok" {
				err = errors.New("gateway tunnel readiness confirmation failed")
			}
		}
		if stream != nil {
			_ = stream.Close()
		}
		if err == nil && string(message[:]) != "ready" {
			err = errors.New("gateway tunnel readiness handshake failed")
		}
		result <- err
	}()
	timer := time.NewTimer(setupTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		_ = session.Close()
		return errors.New("gateway tunnel readiness timed out")
	}
}

func yamuxConfig() *yamux.Config {
	config := yamux.DefaultConfig()
	config.LogOutput = io.Discard
	config.EnableKeepAlive = true
	config.KeepAliveInterval = 25 * time.Second
	return config
}

func acceptStreams(session *yamux.Session, to int, terminal chan<- error, events chan<- Event) {
	for {
		stream, err := session.Accept()
		if err != nil {
			return
		}
		var kind [1]byte
		if _, err = io.ReadFull(stream, kind[:]); err != nil {
			_ = stream.Close()
			continue
		}
		switch kind[0] {
		case streamRequest:
			go forwardLocal(stream, to, events)
		case streamActivity:
			go receiveActivity(stream, events)
		case streamRelease:
			if _, err = stream.Write([]byte{streamAck}); err == nil {
				terminal <- &APIError{
					Status: http.StatusGone,
					Code:   "claim_released",
					Detail: "the tunnel was explicitly released",
				}
			}
			_ = stream.Close()
			_ = session.Close()
			return
		case streamExpire:
			if _, err = stream.Write([]byte{streamAck}); err == nil {
				terminal <- &APIError{
					Status: http.StatusGone,
					Code:   "claim_expired",
					Detail: "the claim reached its maximum duration",
				}
			}
			_ = stream.Close()
			_ = session.Close()
			return
		default:
			_ = stream.Close()
		}
	}
}

func receiveActivity(stream net.Conn, events chan<- Event) {
	defer func() { _ = stream.Close() }()
	var activity activityMessage
	if err := json.NewDecoder(io.LimitReader(stream, 64<<10)).Decode(&activity); err != nil {
		return
	}
	if activity.Method == "" || activity.Path == "" || activity.Status < 100 || activity.Status > 999 || activity.DurationMS < 0 {
		return
	}
	select {
	case events <- Event{
		Type:     EventRequestActivity,
		Method:   activity.Method,
		Path:     activity.Path,
		Status:   activity.Status,
		Duration: time.Duration(activity.DurationMS) * time.Millisecond,
	}:
	default:
	}
}

func forwardLocal(stream net.Conn, to int, events chan<- Event) {
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(to))
	local, err := net.DialTimeout("tcp", target, time.Second)
	if err != nil {
		select {
		case events <- Event{Type: EventLocalTargetError, Err: fmt.Errorf("dial %s: %w", target, err)}:
		default:
		}
		_ = stream.Close()
		return
	}
	bridge(stream, local)
}

func bridge(a, b net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	<-done
	_ = a.Close()
	_ = b.Close()
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
