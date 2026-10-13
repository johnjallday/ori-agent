package usagelimits

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxSessionFiles = 20
	maxSessionTail  = 4 * 1024 * 1024
)

var errNoCodexLimits = errors.New("no Codex session has reported limits yet")

type codexLogWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

type codexLogLimits struct {
	Primary     *codexLogWindow `json:"primary"`
	Secondary   *codexLogWindow `json:"secondary"`
	Plan        string          `json:"plan_type"`
	Credits     *Credits        `json:"credits"`
	ReachedType json.RawMessage `json:"rate_limit_reached_type"`
}

func codexProvider() Provider {
	return Provider{Provider: "codex", DisplayName: "Codex", Level: LevelOK, Windows: []Window{}}
}

// ReadCodexSessionLog inspects at most the twenty newest regular rollout files.
// Each tail is bounded to 4 MiB so large transcripts cannot exhaust memory or
// turn a passive read into a full history scan. Truncated final lines are ignored.
func ReadCodexSessionLog(ctx context.Context, home string) (Provider, error) {
	type sessionFile struct {
		path     string
		modified time.Time
	}
	files := make([]sessionFile, 0, maxSessionFiles+1)
	err := filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil // An unreadable/missing directory is not a usable source.
		}
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), "rollout-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		files = append(files, sessionFile{path, info.ModTime()})
		sort.Slice(files, func(i, j int) bool {
			if files[i].modified.Equal(files[j].modified) {
				return files[i].path > files[j].path
			}
			return files[i].modified.After(files[j].modified)
		})
		if len(files) > maxSessionFiles {
			files = files[:maxSessionFiles]
		}
		return nil
	})
	if err != nil {
		return Provider{}, err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return Provider{}, err
		}
		data, err := readSessionTail(file.path)
		if err != nil {
			continue
		}
		for len(data) > 0 {
			if err := ctx.Err(); err != nil {
				return Provider{}, err
			}
			end := bytes.LastIndexByte(data, '\n')
			line := data[end+1:]
			if p, ok := parseCodexLogEvent(line); ok {
				return p, nil
			}
			if end < 0 {
				break
			}
			data = data[:end]
		}
	}
	return Provider{}, errNoCodexLimits
}

func readSessionTail(path string) ([]byte, error) {
	// #nosec G304 -- path is a regular rollout file found under the trusted local CODEX_HOME.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := min(info.Size(), int64(maxSessionTail))
	offset := info.Size() - size
	data := make([]byte, size)
	n, err := file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	data = data[:n]
	if offset > 0 {
		// Do not try to decode a partial first line at the tail boundary.
		if index := bytes.IndexByte(data, '\n'); index >= 0 {
			data = data[index+1:]
		} else {
			return nil, nil
		}
	}
	return data, nil
}

func parseCodexLogEvent(line []byte) (Provider, bool) {
	var event struct {
		Timestamp time.Time `json:"timestamp"`
		Type      string    `json:"type"`
		Payload   struct {
			Type   string          `json:"type"`
			Limits *codexLogLimits `json:"rate_limits"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type != "event_msg" || event.Payload.Type != "token_count" || event.Payload.Limits == nil || event.Timestamp.IsZero() {
		return Provider{}, false
	}
	limits := event.Payload.Limits
	p := codexProvider()
	p.State, p.Source, p.FetchedAt = StateOK, SourceCodexSessionLog, event.Timestamp
	p.Plan, p.Credits = limits.Plan, limits.Credits
	for _, raw := range []*codexLogWindow{limits.Primary, limits.Secondary} {
		if raw == nil {
			continue
		}
		if raw.WindowMinutes <= 0 || raw.UsedPercent < 0 || math.IsNaN(raw.UsedPercent) || math.IsInf(raw.UsedPercent, 0) {
			return Provider{}, false
		}
		w := WindowFor(raw.WindowMinutes)
		w.UsedPercent, w.ResetsAt = raw.UsedPercent, raw.ResetsAt
		p.Windows = append(p.Windows, w)
	}
	reached := len(limits.ReachedType) > 0 && string(limits.ReachedType) != "null"
	p.summarize(reached)
	if len(p.Windows) == 0 && !reached {
		p.State, p.Detail = StateNoLimits, "This Codex account has no subscription usage windows"
	}
	return p, true
}
