package usagelimits

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeSession(t *testing.T, home, name string, data []byte, modified time.Time) {
	t.Helper()
	path := filepath.Join(home, "sessions", "2026", "10", "13", name)
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSessionLogProlite(t *testing.T) {
	home := t.TempDir()
	writeSession(t, home, "rollout-weekly.jsonl", fixture(t, "codex-prolite.jsonl"), time.Now())
	p, err := ReadCodexSessionLog(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != StateOK || p.Plan != "prolite" || len(p.Windows) != 1 || p.Windows[0].ID != "weekly" || p.Windows[0].UsedPercent != 16 || p.Source != SourceCodexSessionLog {
		t.Fatalf("provider = %+v", p)
	}
	if p.Credits == nil || p.Credits.Balance != "0" || p.FetchedAt.Format(time.RFC3339Nano) != "2026-10-13T01:08:31.053Z" {
		t.Fatalf("credits/time = %+v", p)
	}
}

func TestCodexSessionLogNewestEventAndTruncatedLine(t *testing.T) {
	home := t.TempDir()
	data := append(fixture(t, "codex-prolite.jsonl"), fixture(t, "codex-both-windows.jsonl")...)
	data = append(data, []byte("{\"timestamp\":\"truncated")...)
	writeSession(t, home, "rollout-both.jsonl", data, time.Now())
	p, err := ReadCodexSessionLog(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if p.Plan != "pro" || p.Level != LevelExhausted || len(p.Windows) != 2 || p.Windows[0].ID != "five_hour" || p.Windows[1].Level != LevelWarn {
		t.Fatalf("provider = %+v", p)
	}
}

func TestCodexSessionLogNoLimits(t *testing.T) {
	for _, data := range []string{
		`{"timestamp":"2026-10-13T01:08:31Z","type":"event_msg","payload":{"type":"token_count","rate_limits":null}}`,
		`{"timestamp":"2026-10-13T01:08:31Z","type":"event_msg","payload":{"type":"token_count"}}`,
		`{"timestamp":"2026-10-13T01:08:31Z","type":"response_item","payload":{"type":"token_count","rate_limits":{}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{}}}`,
	} {
		home := t.TempDir()
		writeSession(t, home, "rollout-empty.jsonl", []byte(data), time.Now())
		if _, err := ReadCodexSessionLog(context.Background(), home); !errors.Is(err, errNoCodexLimits) {
			t.Fatalf("expected no limits, got %v", err)
		}
	}
}

func TestCodexSessionLogNewestTwentyOnly(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeSession(t, home, "rollout-old.jsonl", fixture(t, "codex-prolite.jsonl"), now.Add(-time.Hour))
	for i := range 20 {
		writeSession(t, home, fmt.Sprintf("rollout-%02d.jsonl", i), []byte("{}\n"), now.Add(time.Duration(i)*time.Second))
	}
	if _, err := ReadCodexSessionLog(context.Background(), home); !errors.Is(err, errNoCodexLimits) {
		t.Fatalf("oldest (21st) file should not be read: %v", err)
	}
	writeSession(t, home, "rollout-new.jsonl", fixture(t, "codex-both-windows.jsonl"), now.Add(time.Minute))
	p, err := ReadCodexSessionLog(context.Background(), home)
	if err != nil || p.Plan != "pro" {
		t.Fatalf("newest file = %+v, %v", p, err)
	}
}

func TestCodexSessionLogBoundedTail(t *testing.T) {
	home := t.TempDir()
	data := append([]byte(strings.Repeat("x", maxSessionTail+100)+"\n"), fixture(t, "codex-prolite.jsonl")...)
	writeSession(t, home, "rollout-large.jsonl", data, time.Now())
	p, err := ReadCodexSessionLog(context.Background(), home)
	if err != nil || p.Plan != "prolite" {
		t.Fatalf("tail = %+v, %v", p, err)
	}
}

func TestCodexSessionLogCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadCodexSessionLog(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
