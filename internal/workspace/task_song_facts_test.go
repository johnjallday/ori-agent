package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// songFactsSpy answers for one workspace and one agent, as the server's reader
// does, and records every question it was asked.
type songFactsSpy struct {
	asked []string
}

func (s *songFactsSpy) SongFacts(_ context.Context, workspaceID, agentName string) string {
	s.asked = append(s.asked, workspaceID+"|"+agentName)
	if workspaceID == "music" && agentName == "Music Manager" {
		return "## Song Facts\n\n<<<SONG-FACTS-abc\n- Night Drive · 92 BPM\nSONG-FACTS-abc>>>"
	}
	return ""
}

func songFactsHandler(t *testing.T, spy SongFactsReader) *LLMTaskHandler {
	t.Helper()
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Music"})
	ws.ID = "music"
	ws.OwnerUserID = userprofile.LocalUserID
	ws.AgentInstances = []AgentInstance{
		{ID: "entry", Name: "Music Manager", EntryPoint: true},
		{ID: "helper", Name: "Helper"},
	}
	if err := folders.Save(ws); err != nil {
		t.Fatal(err)
	}
	return &LLMTaskHandler{workspaceStore: folders, songFacts: spy}
}

func segmentLabels(segs []promptSegment) []string {
	labels := make([]string, 0, len(segs))
	for _, seg := range segs {
		labels = append(labels, seg.label)
	}
	return labels
}

func TestTaskPrompt_SongFactsAreGivenToTheAgentTheReaderNames(t *testing.T) {
	spy := &songFactsSpy{}
	h := songFactsHandler(t, spy)
	task := Task{ID: "t1", WorkspaceID: "music", To: "Music Manager", Description: "Write a note on Night Drive."}
	segs := h.buildTaskPromptSegments(context.Background(), task)

	var found *promptSegment
	for i := range segs {
		if segs[i].label == "song_facts" {
			found = &segs[i]
		}
	}
	if found == nil {
		t.Fatalf("no song_facts segment among %v", segmentLabels(segs))
	}
	if !strings.Contains(found.text, "Night Drive · 92 BPM") || found.trimOrder == 0 {
		t.Fatalf("segment = %+v: the facts must be there and trimmable (never protected)", *found)
	}
	// The facts sit inside the user turn, after the header that carries the task.
	rendered := renderPromptSegments(segs)
	if strings.Index(rendered, "Write a note on Night Drive.") > strings.Index(rendered, "Night Drive · 92 BPM") {
		t.Fatal("the song facts come before the task description")
	}
	if len(spy.asked) != 1 || spy.asked[0] != "music|Music Manager" {
		t.Fatalf("the reader was asked %v", spy.asked)
	}
}

// Only the folder's own agent: another agent of the same workspace, a name that is
// not attached to it, a task with no workspace, and a handler with no reader all
// get no section, and the reader is never asked about a name the workspace does
// not hold.
func TestTaskPrompt_SongFactsAreNotGivenToAnyoneElse(t *testing.T) {
	spy := &songFactsSpy{}
	h := songFactsHandler(t, spy)
	ctx := context.Background()
	for name, task := range map[string]Task{
		"another agent of the workspace": {ID: "t2", WorkspaceID: "music", To: "Helper"},
		"a name nobody holds":            {ID: "t3", WorkspaceID: "music", To: "Library Manager"},
		"another workspace":              {ID: "t4", WorkspaceID: "elsewhere", To: "Music Manager"},
		"no workspace":                   {ID: "t5", To: "Music Manager"},
		"no assignee":                    {ID: "t6", WorkspaceID: "music"},
	} {
		if got := h.taskSongFactsSection(ctx, task); got != "" {
			t.Errorf("%s was given %q", name, got)
		}
		for _, label := range segmentLabels(h.buildTaskPromptSegments(ctx, task)) {
			if label == "song_facts" {
				t.Errorf("%s has a song_facts segment", name)
			}
		}
	}
	for _, asked := range spy.asked {
		if strings.HasSuffix(asked, "|Library Manager") || strings.HasPrefix(asked, "elsewhere|") {
			t.Errorf("the reader was asked about %q, which the workspace does not hold", asked)
		}
	}
	bare := &LLMTaskHandler{workspaceStore: h.workspaceStore}
	if got := bare.taskSongFactsSection(ctx, Task{WorkspaceID: "music", To: "Music Manager"}); got != "" {
		t.Errorf("a handler with no reader gave %q", got)
	}
}
