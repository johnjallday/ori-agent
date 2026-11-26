//go:build darwin || linux

package filejanitor

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type stationChatFunc func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)

func (f stationChatFunc) Chat(ctx context.Context, r llm.ChatRequest) (*llm.ChatResponse, error) {
	return f(ctx, r)
}

func TestStationLinesOnlyReadExactApprovedVersionsAndNeverFile(t *testing.T) {
	f := newDocsFixture(t, "brief.txt", "photo.png", "tax-return.txt")
	f.mkdir("Trips")
	w, _ := f.s.workspaces.Get("ws-1")
	w.AgentInstances = []workspace.AgentInstance{{ID: "curator", Name: "Manager"}}
	w.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "curator", Shared: true})
	_, err := f.s.store.UpdateSettings("ws-1", func(v *JanitorSettings) error {
		v.FilingMethod = MethodFolders
		v.MethodRevision = "fixture"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Prepare a text-shaped source with the scan's name, size and mtime. The
	// preview must independently pin the current version (including change time).
	path := f.abs("brief.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f.write("brief.txt", "Trip plans")
	if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	p, err := f.s.ReadShelfPicture(ctx, "local", "ws-1", f.batch.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	req := StationReadRequest{Revision: p.Revision}
	for _, file := range p.Files {
		req.Files = append(req.Files, file.ID)
	}
	preview := func() StationReadReview {
		t.Helper()
		r, err := f.s.PreviewStationRead(ctx, "local", "ws-1", f.batch.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	run := func(token string) (StationReadResult, error) {
		return f.s.ReadStationLines(ctx, "local", "ws-1", f.batch.ID, token)
	}
	reads, calls := 0, 0
	f.s.stationRead.read = func(root, name string, n int64, id string, v workspace.SourceVersion) (workspace.ContainedFile, error) {
		reads++
		if name != "brief.txt" {
			t.Fatalf("unapproved read %s", name)
		}
		return workspace.ReadVersionedFile(root, name, n, id, v)
	}
	if no := preview(); no.Reason == "" || no.Approval != "" || reads != 0 {
		t.Fatalf("no-model preview: %+v", no)
	}
	proc := StationProcessor{Provider: "Example cloud", Model: "test", Pin: "original", OffDevice: true}
	reply := `[{"file":1,"folder":1}]`
	f.s.SetStationReadHost(func(id, lead string) (StationProcessor, StationChat) {
		return proc, stationChatFunc(func(_ context.Context, r llm.ChatRequest) (*llm.ChatResponse, error) {
			calls++
			if !r.Isolated || !r.SingleAttempt || r.ProviderConfiguration != proc.Pin || len(r.Tools) != 0 {
				t.Fatal("unexpected provider request")
			}
			data, _ := json.Marshal(r.Messages)
			if !strings.Contains(string(data), "Trip plans") || strings.Contains(string(data), f.root) || strings.Contains(string(data), "brief.txt") {
				t.Fatal("unexpected disclosure")
			}
			return &llm.ChatResponse{Content: reply, FinishReason: "stop"}, nil
		})
	}, nil)
	before := f.tree()
	settings, _ := f.s.store.LoadSettings("ws-1")
	first := preview()
	if reads != 0 || calls != 0 || first.Approval == "" || !reflect.DeepEqual(first.Files, []string{"brief.txt"}) || first.Remaining != 2 || !first.Processor.OffDevice {
		t.Fatalf("metadata review: %+v", first)
	}
	if _, err = f.s.ReadStationLines(ctx, "foreign", "ws-1", f.batch.ID, first.Approval); err == nil {
		t.Fatal("wrong owner admitted")
	}
	got, err := run(first.Approval)
	if err != nil || reads != 1 || calls != 1 || got.Read != 1 || got.Remaining != 2 || len(got.Placements) != 1 {
		t.Fatalf("result: %+v %v reads=%d calls=%d", got, err, reads, calls)
	}
	if _, err = run(first.Approval); err == nil || reads != 1 {
		t.Fatal("replayed read")
	}
	next := preview()
	proc.Pin = "changed"
	if _, err = run(next.Approval); err == nil || reads != 1 || calls != 1 {
		t.Fatal("changed processor received text")
	}
	next = preview()
	reply = `[{"file":1,"folder":999}]`
	if _, err = run(next.Approval); err == nil {
		t.Fatal("invented destination accepted")
	}
	if !reflect.DeepEqual(before, f.tree()) {
		t.Fatal("read changed a file")
	}
	after, _ := f.s.store.LoadSettings("ws-1")
	if !reflect.DeepEqual(settings, after) {
		t.Fatal("read widened persistent settings")
	}
	next = preview()
	f.s.now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	if _, err = run(next.Approval); err == nil || reads != 2 {
		t.Fatal("expired review read a file")
	}
	next = preview()
	if err = os.Rename(f.root, f.root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(f.root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err = run(next.Approval); err == nil || reads != 2 || calls != 2 {
		t.Fatal("replacement folder authorized")
	}
	if err = os.Remove(f.root); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(f.root+"-original", f.root); err != nil {
		t.Fatal(err)
	}
	next = preview()
	w.AgentInstances = nil
	if _, err = run(next.Approval); err == nil || reads != 2 {
		t.Fatal("withdrawn manager authorized")
	}
	w.AgentInstances = []workspace.AgentInstance{{ID: "curator", Name: "Manager"}}
	next = preview()
	f.write("brief.txt", "replacement")
	if _, err = run(next.Approval); err == nil || reads != 2 {
		t.Fatal("changed file reached the reader")
	}
}
