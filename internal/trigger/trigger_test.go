package trigger

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateTokenProperties(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		if len(tok) < 40 {
			t.Fatalf("token too short: %d chars", len(tok))
		}
		if strings.ContainsAny(tok, "+/=") {
			t.Fatalf("token not URL-safe: %q", tok)
		}
		if seen[tok] {
			t.Fatal("duplicate token generated")
		}
		seen[tok] = true
	}
}

func TestSecureCompare(t *testing.T) {
	if !SecureCompare("abc", "abc") {
		t.Error("equal strings must compare true")
	}
	if SecureCompare("abc", "abd") || SecureCompare("abc", "abcd") || SecureCompare("", "a") {
		t.Error("unequal strings must compare false")
	}
	if !SecureCompare("", "") {
		t.Error("empty strings are equal")
	}
}

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		mutate  func(*Trigger)
		wantErr bool
	}{
		{"valid webhook mission_run", func(tr *Trigger) {}, false},
		{"missing name", func(tr *Trigger) { tr.Name = " " }, true},
		{"missing workspace", func(tr *Trigger) { tr.WorkspaceID = "" }, true},
		{"unknown action", func(tr *Trigger) { tr.Action.Kind = "explode" }, true},
		{"task_prompt without prompt", func(tr *Trigger) {
			tr.Action = Action{Kind: ActionTaskPrompt, Agent: "writer"}
		}, true},
		{"task_prompt without agent", func(tr *Trigger) {
			tr.Action = Action{Kind: ActionTaskPrompt, Prompt: "do it"}
		}, true},
		{"valid task_prompt", func(tr *Trigger) {
			tr.Action = Action{Kind: ActionTaskPrompt, Agent: "writer", Prompt: "do it"}
		}, false},
		{"webhook without token", func(tr *Trigger) { tr.Webhook = &WebhookConfig{} }, true},
		{"file_watch relative path", func(tr *Trigger) {
			tr.Type = TypeFileWatch
			tr.Webhook = nil
			tr.FileWatch = &FileWatchConfig{Path: "relative/dir"}
		}, true},
		{"file_watch bad glob", func(tr *Trigger) {
			tr.Type = TypeFileWatch
			tr.Webhook = nil
			tr.FileWatch = &FileWatchConfig{Path: dir, Glob: "[unclosed"}
		}, true},
		{"file_watch bad event type", func(tr *Trigger) {
			tr.Type = TypeFileWatch
			tr.Webhook = nil
			tr.FileWatch = &FileWatchConfig{Path: dir, Events: []string{"explode"}}
		}, true},
		{"valid file_watch", func(tr *Trigger) {
			tr.Type = TypeFileWatch
			tr.Webhook = nil
			tr.FileWatch = &FileWatchConfig{Path: dir, Glob: "*.pdf", Events: []string{"create", "modify"}}
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := Trigger{
				WorkspaceID: "ws1",
				Name:        "t",
				Type:        TypeWebhook,
				Action:      Action{Kind: ActionMissionRun},
				Webhook:     &WebhookConfig{Token: "tok"},
			}
			tc.mutate(&tr)
			err := tr.Validate()
			if tc.wantErr && err == nil {
				t.Error("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("want nil, got %v", err)
			}
		})
	}
}

func TestCheckWatchPath(t *testing.T) {
	dir := t.TempDir()
	tr := Trigger{
		WorkspaceID: "ws1", Name: "w", Type: TypeFileWatch,
		Action:    Action{Kind: ActionMissionRun},
		FileWatch: &FileWatchConfig{Path: dir},
	}
	if err := tr.CheckWatchPath(); err != nil {
		t.Errorf("existing dir should pass: %v", err)
	}
	tr.FileWatch.Path = dir + "/gone"
	if err := tr.CheckWatchPath(); err == nil {
		t.Error("missing dir should fail")
	}
}

func TestMatchesFileEvent(t *testing.T) {
	tr := Trigger{
		Type:      TypeFileWatch,
		FileWatch: &FileWatchConfig{Path: "/x", Glob: "*.pdf"},
	}
	// Default event set is create-only.
	if !tr.MatchesFileEvent("create", "invoice.pdf") {
		t.Error("create+glob match expected")
	}
	if tr.MatchesFileEvent("modify", "invoice.pdf") {
		t.Error("modify should not match default create-only set")
	}
	if tr.MatchesFileEvent("create", "notes.txt") {
		t.Error("glob should exclude non-matching names")
	}

	tr.FileWatch.Events = []string{"create", "remove"}
	if !tr.MatchesFileEvent("remove", "invoice.pdf") {
		t.Error("configured remove event should match")
	}
}

func TestSummary(t *testing.T) {
	now := time.Now()
	fileEvents := []Event{
		{Kind: "file", FileEvent: "create", FileName: "a.pdf", Timestamp: now},
		{Kind: "file", FileEvent: "create", FileName: "b.pdf", Timestamp: now},
		{Kind: "file", FileEvent: "create", FileName: "c.pdf", Timestamp: now},
	}
	got := Summary(fileEvents, 0)
	if got != "create: a.pdf (+2 more)" {
		t.Errorf("file summary = %q", got)
	}

	web := []Event{{Kind: "webhook", Body: strings.Repeat("x", 1228), RemoteAddr: "192.168.1.10"}}
	got = Summary(web, 0)
	if got != "POST 1.2 KB from 192.168.1.10" {
		t.Errorf("webhook summary = %q", got)
	}

	if Summary(nil, 0) != "no events" {
		t.Error("empty summary")
	}

	// Dropped events count toward the total.
	got = Summary(fileEvents[:1], 4)
	if got != "create: a.pdf (+4 more)" {
		t.Errorf("dropped-events summary = %q", got)
	}
}

func TestRecordFireTracksFailures(t *testing.T) {
	tr := Trigger{Name: "t"}
	tr.RecordFire(FireRecord{FireID: "1", Error: "boom"})
	if tr.FailureCount != 1 || tr.LastError != "boom" {
		t.Errorf("failure not tracked: %+v", tr)
	}
	tr.RecordFire(FireRecord{FireID: "2", RunID: "run-1"})
	if tr.LastError != "" {
		t.Error("success should clear LastError")
	}
	if tr.FireCount != 2 {
		t.Errorf("fire count = %d, want 2", tr.FireCount)
	}
}
