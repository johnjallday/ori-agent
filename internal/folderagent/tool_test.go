package folderagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestLookTool_OpensAFolderOfTheMapAndRecordsIt(t *testing.T) {
	var paths []string
	for i := range 6 {
		paths = append(paths, fmt.Sprintf("Box %d/a.txt", i))
	}
	paths = append(paths, "Box 5/Song/Song.rpp")
	h := newHarness(t, build(t, paths...))
	h.svc.deps.Walk = foldermap.Options{MaxEntries: 6}
	view := h.start(t, "")

	if h.svc.LookTool("not-a-folder-workspace") != nil {
		t.Fatal("a workspace without a folder record gets no folder_look")
	}
	tool := h.svc.LookTool(view.WorkspaceID)
	if tool == nil || tool.Definition().Name != folderlook.LookToolName {
		t.Fatalf("tool = %v", tool)
	}
	for _, bad := range []string{`{"folder":"/etc"}`, `{"folder":"../x"}`, `{"path":"Box 5"}`, `{}`} {
		if _, err := tool.Call(context.Background(), bad); err == nil {
			t.Errorf("Call(%s) was allowed", bad)
		}
	}
	out, err := tool.Call(context.Background(), `{"folder":"Box 5"}`)
	if err != nil {
		t.Fatal(err)
	}
	var result foldermap.LookResult
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Folder != "Box 5" {
		t.Fatalf("result = %s (%v)", out, err)
	}
	if strings.Contains(out, "/private") || strings.Contains(out, "/var/") {
		t.Fatalf("the result names an absolute path: %s", out)
	}
	if _, err := tool.Call(context.Background(), `{"folder":"Box 5/Song"}`); err != nil {
		t.Fatal(err)
	}
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	if len(rec.Facts.Members) != 1 || rec.Facts.Members[0].Path != "Box 5/Song" || rec.Stage != foldermap.StageTools {
		t.Fatalf("facts after the tool's looks = %+v stage %q", rec.Facts.Members, rec.Stage)
	}
}
