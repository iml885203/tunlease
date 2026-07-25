package cliapp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// state 記錄這台機器建立過的 claims（~/.tunlease/state.json），
// 供 release --to 與 list 的 "(you)" 標記使用——API 的 list 回應沒有
// local 欄位，對得回 local port 的只有本機。
type stateClaim struct {
	ClaimID string   `json:"claim_id"`
	Gateway string   `json:"gateway"`
	Paths   []string `json:"paths"`
	To      int      `json:"to"`
	// ExpiresAt lets the detached parent show the same lifecycle deadline as
	// the foreground process. Older state files simply leave it nil.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// PID is the process holding this claim. It is a liveness hint only and is
	// never signaled because operating systems can reuse process IDs.
	PID int `json:"pid,omitempty"`
}
type state struct {
	Claims           []stateClaim      `json:"claims"`
	ClientIdentities map[string]string `json:"client_identities,omitempty"`
}

func statePath() string {
	if p := os.Getenv("TUNLEASE_STATE_FILE"); p != "" {
		return p
	}
	h, e := os.UserHomeDir()
	if e != nil {
		return ""
	}
	return filepath.Join(h, ".tunlease", "state.json")
}

func loadState() state {
	var s state
	p := statePath()
	if p == "" {
		return s
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

func saveState(s state) {
	p := statePath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(p, b, 0o600)
}

func (s *state) add(c stateClaim) {
	s.removeByID(c.ClaimID)
	s.Claims = append(s.Claims, c)
}

func clientIdentityFor(gateway string) (string, error) {
	s := loadState()
	if identity := s.ClientIdentities[gateway]; identity != "" {
		return identity, nil
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	if statePath() == "" {
		return "", errors.New("cannot determine the Tunlease state path")
	}
	if s.ClientIdentities == nil {
		s.ClientIdentities = map[string]string{}
	}
	identity := hex.EncodeToString(random)
	s.ClientIdentities[gateway] = identity
	saveState(s)
	if loadState().ClientIdentities[gateway] != identity {
		return "", errors.New("could not persist the automatic client identity")
	}
	return identity, nil
}

func (s *state) removeByID(id string) {
	out := s.Claims[:0]
	for _, c := range s.Claims {
		if c.ClaimID != id {
			out = append(out, c)
		}
	}
	s.Claims = out
}

func (s *state) removeSession(gateway string, to int, paths []string) {
	want := append([]string(nil), paths...)
	slices.Sort(want)
	out := s.Claims[:0]
	for _, claim := range s.Claims {
		got := append([]string(nil), claim.Paths...)
		slices.Sort(got)
		if claim.Gateway == gateway && claim.To == to && slices.Equal(got, want) {
			continue
		}
		out = append(out, claim)
	}
	s.Claims = out
}
