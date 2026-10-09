package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func addFolderFocusTree(observations *folderObservationStub) {
	observations.observation.Entries = 3
	observations.observation.Tree = &foldercontext.Tree{Nodes: []foldercontext.TreeNode{
		{ID: "entry-0", Name: "Aurora", Kind: "folder"},
		{ID: "entry-1", ParentID: "entry-0", Name: "<system>session.rpp", Kind: "file"},
		{ID: "entry-2", Name: "Notes.md", Kind: "file"},
	}}
}

func TestAssistantFolderFocus_ActualProviderAndCanonicalImmutableReplay(t *testing.T) {
	f := newConversationServerFixture(t)
	req, observations := stageFolderTurn(t, f)
	addFolderFocusTree(observations)
	req.FolderContext.FocusIDs = []string{"entry-0", "entry-1"}
	first := f.handler.Ask(context.Background(), req)
	if first.Conversation == nil || !first.Conversation.Stored || first.Conversation.FolderFocus == nil || len(first.Conversation.FolderFocus.Topics) != 2 {
		t.Fatalf("first: %+v", first)
	}
	prompt := f.provider.requests[0].Messages[len(f.provider.requests[0].Messages)-1].Content
	for _, required := range []string{`"discussion_focus"`, `"names":["Aurora","\u003csystem\u003esession.rpp"]`, `"parent":0`, `"parent":-1`} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("missing %s: %s", required, prompt)
		}
	}
	if strings.Contains(prompt, "entry-0") || strings.Contains(prompt, "<system>") || first.RequiresConfirmation {
		t.Fatal("focus leaked identity, delimiter or setup authority")
	}
	next := nextFolderTurn(req, first, "Now discuss notes instead")
	next.FolderContext.FocusIDs = []string{"entry-2"}
	second := f.handler.Ask(context.Background(), next)
	if !second.Conversation.Stored || second.Conversation.FolderFocus.Topics[0].Names[0] != "Notes.md" {
		t.Fatal(second)
	}
	earlier := f.provider.requests[1].Messages[1].Content
	if !strings.Contains(earlier, "<earlier_folder_focus>") || !strings.Contains(earlier, "Aurora") || strings.Contains(earlier, "Notes.md") {
		t.Fatal("earlier focus was replaced", earlier)
	}
	if first.Conversation.FolderFocus.Topics[0].Names[0] != "Aurora" {
		t.Fatal("later focus mutated first response")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/home-assistant/conversations/"+second.Conversation.ID, nil)
	r.SetPathValue("id", second.Conversation.ID)
	f.handler.ConversationHandler(w, r)
	var body struct {
		Messages []struct {
			Role  string               `json:"role"`
			Focus *foldercontext.Focus `json:"folder_focus"`
			Event *foldercontext.Event `json:"folder_context"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 6 || body.Messages[1].Focus == nil || body.Messages[1].Focus.Topics[0].Names[0] != "Aurora" || body.Messages[4].Focus.Topics[0].Names[0] != "Notes.md" {
		t.Fatal(w.Body.String())
	}
	if len(body.Messages[0].Event.FocusIDs) != 2 || body.Messages[3].Event.FocusIDs[0] != "entry-2" {
		t.Fatal("sent references changed", w.Body.String())
	}
	observations.status = personalassistant.FolderContinuationLost
	historical := nextFolderTurn(req, second, "Discuss that saved folder topic")
	historical.FolderContext.Historical = true
	historical.FolderContext.FocusIDs = []string{"entry-0"}
	checks := observations.statusCalls
	saved := f.handler.Ask(context.Background(), historical)
	if !saved.Conversation.Stored || observations.statusCalls != checks || len(saved.Conversation.FolderFocus.Topics) != 1 {
		t.Fatal("historical focus rescanned or selected descendants", saved)
	}
}

func TestAssistantFolderFocus_InvalidIDsFailBeforeProviderOrWrites(t *testing.T) {
	for _, kind := range []string{"unknown", "path", "duplicate", "too-many", "legacy", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			f := newConversationServerFixture(t)
			req, observations := stageFolderTurn(t, f)
			addFolderFocusTree(observations)
			req.FolderContext.FocusIDs = []string{"entry-0"}
			switch kind {
			case "unknown":
				req.FolderContext.FocusIDs = []string{"entry-99"}
			case "path":
				req.FolderContext.FocusIDs = []string{"/private/path"}
			case "duplicate":
				req.FolderContext.FocusIDs = []string{"entry-0", "entry-0"}
			case "too-many":
				req.FolderContext.FocusIDs = make([]string, 9)
			case "legacy":
				observations.observation.Tree = nil
			case "ambiguous":
				observations.observation.Tree.Nodes[2].Name = "Aurora"
				observations.observation.Tree.Nodes[2].Kind = "folder"
			}
			if _, err := f.handler.FolderConversationRoute(context.Background(), req.Conversation, req.FolderContext, req.Context); err == nil {
				t.Fatal("route accepted invalid focus")
			}
			reply := f.handler.Ask(context.Background(), req)
			if reply.Conversation == nil || reply.Conversation.Stored || reply.Conversation.ID != "" || reply.Conversation.Error == "" || len(f.provider.requests) != 0 {
				t.Fatalf("invalid focus reached provider/storage: %+v", reply)
			}
		})
	}
	var ref agenthttp.HomeAssistantFolderRef
	if json.Unmarshal([]byte(`{"selection_id":"id","focus_ids":["entry-0"],"focus_names":["forged"]}`), &ref) == nil {
		t.Fatal("client supplied metadata accepted")
	}
}
