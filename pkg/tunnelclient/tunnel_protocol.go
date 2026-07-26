package tunnelclient

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

type activityMessage struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}
