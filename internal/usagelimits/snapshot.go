// Package usagelimits reads subscription usage windows without refreshing or
// persisting provider credentials. It is separate from token/cost accounting.
package usagelimits

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

type State string

type Level string

type Source string

const (
	StateOK          State = "ok"
	StateNotSignedIn State = "not_signed_in"
	StateNoLimits    State = "no_limits"
	StateUnavailable State = "unavailable"

	LevelOK        Level = "ok"
	LevelWarn      Level = "warn"
	LevelExhausted Level = "exhausted"

	SourceCodexSessionLog  Source = "codex_session_log"
	SourceCodexAppServer   Source = "codex_app_server"
	SourceClaudeOAuthUsage Source = "claude_oauth_usage"
	SourceClaudeStatusline Source = "claude_statusline_mirror"
)

type Snapshot struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Providers   []Provider `json:"providers"`
}

// Provider deliberately contains no credential or account identity fields.
type Provider struct {
	Provider    string        `json:"provider"`
	DisplayName string        `json:"display_name"`
	State       State         `json:"state"`
	Level       Level         `json:"level"`
	Plan        string        `json:"plan,omitempty"`
	Windows     []Window      `json:"windows"`
	Credits     *Credits      `json:"credits,omitempty"`
	FetchedAt   time.Time     `json:"fetched_at,omitzero"`
	Source      Source        `json:"source,omitempty"`
	Detail      string        `json:"detail"`
	Mirror      *MirrorStatus `json:"mirror,omitempty"`
}

// MirrorStatus deliberately excludes the prior command and local file paths.
type MirrorStatus struct {
	Installed              bool `json:"installed"`
	PreviousCommandPresent bool `json:"previous_command_present"`
}

type Window struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	WindowMinutes int     `json:"window_minutes"`
	UsedPercent   float64 `json:"used_percent"`
	ResetsAt      int64   `json:"resets_at"`
	Level         Level   `json:"level"`
	ModelSpecific bool    `json:"model_specific,omitempty"`
}

type Credits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

// LevelFor classifies utilization; an explicit reached signal takes precedence.
func LevelFor(percent float64, reached bool) Level {
	if reached || percent >= 100 {
		return LevelExhausted
	}
	if percent >= 80 {
		return LevelWarn
	}
	return LevelOK
}

// WindowFor names a window by duration, never by its position in a response.
func WindowFor(minutes int) Window {
	w := Window{WindowMinutes: minutes, Level: LevelOK}
	switch minutes {
	case 300:
		w.ID, w.Label = "five_hour", "5-hour"
	case 10080:
		w.ID, w.Label = "weekly", "Weekly"
	default:
		w.ID = fmt.Sprintf("custom_%d", minutes)
		if minutes > 0 && minutes%1440 == 0 {
			w.Label = fmt.Sprintf("%d-day", minutes/1440)
		} else {
			w.Label = strconv.FormatFloat(float64(minutes)/60, 'f', -1, 64) + "-hour"
		}
	}
	return w
}

// summarize orders windows and applies the source's provider-wide reached flag.
func (p *Provider) summarize(reached bool) {
	sort.SliceStable(p.Windows, func(i, j int) bool {
		return p.Windows[i].WindowMinutes < p.Windows[j].WindowMinutes
	})
	p.Level = LevelFor(0, reached)
	for i := range p.Windows {
		w := &p.Windows[i]
		w.Level = LevelFor(w.UsedPercent, false)
		if w.Level == LevelExhausted || (w.Level == LevelWarn && p.Level == LevelOK) {
			p.Level = w.Level
		}
	}
}
