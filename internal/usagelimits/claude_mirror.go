package usagelimits

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

const ClaudeMirrorFilename = "claude-usage-limits.json"

var errClaudeMirror = errors.New("no recent Claude status line usage reading")

// ReadClaudeMirror reads only subscription windows. Model, workspace, context
// and other status-line metadata must never escape into the public snapshot.
func ReadClaudeMirror(path string, now time.Time) (Provider, error) {
	// #nosec G304 -- fixed mirror filename in Ori's trusted configured data directory.
	file, err := os.Open(path)
	if err != nil {
		return Provider{}, errClaudeMirror
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) > 24*time.Hour {
		return Provider{}, errClaudeMirror
	}
	const maxBody = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(file, maxBody+1))
	if err != nil || len(data) > maxBody {
		return Provider{}, errClaudeMirror
	}
	type mirrorWindow struct {
		Percent  *float64 `json:"used_percentage"`
		ResetsAt int64    `json:"resets_at"`
	}
	var payload struct {
		Limits *struct {
			FiveHour *mirrorWindow `json:"five_hour"`
			SevenDay *mirrorWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.Limits == nil {
		return Provider{}, errClaudeMirror
	}
	p := claudeProvider()
	p.State, p.Source, p.FetchedAt = StateOK, SourceClaudeStatusline, info.ModTime().UTC()
	for _, entry := range []struct {
		minutes int
		raw     *mirrorWindow
	}{{300, payload.Limits.FiveHour}, {10080, payload.Limits.SevenDay}} {
		if entry.raw == nil {
			continue
		}
		if entry.raw.Percent == nil || *entry.raw.Percent < 0 {
			return Provider{}, errClaudeMirror
		}
		w := WindowFor(entry.minutes)
		w.UsedPercent, w.ResetsAt = *entry.raw.Percent, entry.raw.ResetsAt
		p.Windows = append(p.Windows, w)
	}
	if len(p.Windows) == 0 {
		return Provider{}, errClaudeMirror
	}
	p.summarize(false)
	return p, nil
}
