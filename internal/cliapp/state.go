package cliapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

// state 記錄這台機器建立過的 claims（~/.tunlease/state.json），
// 供 release --to 與 list 的 "(you)" 標記使用——API 的 list 回應沒有
// local 欄位，對得回 local port 的只有本機。
type stateClaim struct {
	ClaimID string   `json:"claim_id"`
	Gateway string   `json:"gateway"`
	Paths   []string `json:"paths"`
	To      int      `json:"to"`
	// PID is the detached daemon holding this claim (0 for a foreground claim).
	PID int `json:"pid,omitempty"`
}
type state struct {
	Claims []stateClaim `json:"claims"`
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
