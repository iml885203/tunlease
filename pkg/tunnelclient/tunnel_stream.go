package tunnelclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/yamux"
)

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
