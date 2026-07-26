package tunnelclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

var setupTimeout = 10 * time.Second

type liveTunnel struct {
	session  *yamux.Session
	terminal <-chan error
	events   <-chan Event
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
