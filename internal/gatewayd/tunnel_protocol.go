package gatewayd

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	protocolMajor  = 1
	headerProtocol = "X-Tunlease-Protocol"
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

func requireCompatibleProtocol(w http.ResponseWriter, r *http.Request, gatewayProtocol int) bool {
	clientProtocol := protocolMajor
	if value := r.Header.Get(headerProtocol); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			writeTunnelError(w, http.StatusBadRequest, "invalid_request", "X-Tunlease-Protocol must be a positive integer", nil)
			return false
		}
		clientProtocol = parsed
	}
	w.Header().Set(headerProtocol, strconv.Itoa(gatewayProtocol))
	switch {
	case clientProtocol < gatewayProtocol:
		writeTunnelError(
			w,
			http.StatusUpgradeRequired,
			"client_upgrade_required",
			fmt.Sprintf("gateway protocol %d requires a newer Tunlease client", gatewayProtocol),
			nil,
		)
		return false
	case clientProtocol > gatewayProtocol:
		writeTunnelError(
			w,
			http.StatusUpgradeRequired,
			"gateway_upgrade_required",
			fmt.Sprintf("client protocol %d requires a newer Tunlease gateway", clientProtocol),
			nil,
		)
		return false
	default:
		return true
	}
}

type activityMessage struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

func yamuxConfig() *yamux.Config {
	config := yamux.DefaultConfig()
	config.LogOutput = io.Discard
	config.EnableKeepAlive = true
	config.KeepAliveInterval = 25 * time.Second
	return config
}
