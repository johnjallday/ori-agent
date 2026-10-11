package server

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestAssistantFolderFocus_BulkSubsetReachesProviderAndCanonicalReplay(t *testing.T) {
	f := newConversationServerFixture(t)
	req, observations := stageFolderTurn(t, f)
	o := &observations.observation
	o.Entries = foldercontext.MaxTreeNodes
	o.Tree = &foldercontext.Tree{}
	var ids []string
	for i := 0; i < foldercontext.MaxTreeNodes; i++ {
		node := foldercontext.TreeNode{ID: fmt.Sprintf("entry-%d", i), Name: fmt.Sprintf("Topic %d", i), Kind: "file"}
		if i < 3 {
			node.Kind = "folder"
			node.Name = strings.Repeat("<", foldercontext.MaxNameRunes)
		}
		if i > 0 {
			node.ParentID = fmt.Sprintf("entry-%d", min(i-1, 2))
		}
		o.Tree.Nodes = append(o.Tree.Nodes, node)
		if i != 9 {
			ids = append(ids, node.ID)
		}
	}
	req.FolderContext.FocusIDs = ids
	// Exercise the reference decoder as well as the host's resolved metadata.
	encoded, err := json.Marshal(req.FolderContext)
	if err != nil {
		t.Fatal(err)
	}
	var ref agenthttp.HomeAssistantFolderRef
	if err := json.Unmarshal(encoded, &ref); err != nil {
		t.Fatal("bulk subset rejected by reference decoder", err)
	}
	req.FolderContext = &ref
	if _, err := f.handler.FolderConversationRoute(context.Background(), req.Conversation, req.FolderContext, req.Context); err != nil {
		t.Fatal("bulk subset rejected by Route", err)
	}
	reply := f.handler.Ask(context.Background(), req)
	if reply.Conversation == nil || !reply.Conversation.Stored || reply.Conversation.FolderFocus == nil || len(reply.Conversation.FolderFocus.Topics) != len(ids) {
		t.Fatalf("bulk subset not answered and stored: %+v", reply)
	}
	prompt := f.provider.requests[0].Messages[len(f.provider.requests[0].Messages)-1].Content
	_, data, ok := strings.Cut(prompt, "<folder_observation>")
	if !ok {
		t.Fatal("missing folder metadata")
	}
	data, _, ok = strings.Cut(data, "</folder_observation>")
	if !ok {
		t.Fatal("unterminated folder metadata")
	}
	var sent struct {
		Focus foldercontext.Focus `json:"discussion_focus"`
	}
	if err := json.Unmarshal([]byte(data), &sent); err != nil || len(sent.Focus.Topics) != len(ids) {
		t.Fatal("provider did not receive exact subset", err)
	}
	for _, topic := range sent.Focus.Topics {
		if topic.Names[len(topic.Names)-1] == "Topic 9" {
			t.Fatal("provider focus includes unchecked topic")
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/home-assistant/conversations/"+reply.Conversation.ID, nil)
	r.SetPathValue("id", reply.Conversation.ID)
	f.handler.ConversationHandler(w, r)
	var saved struct {
		Messages []struct {
			Event *foldercontext.Event `json:"folder_context"`
			Focus *foldercontext.Focus `json:"folder_focus"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 3 || saved.Messages[0].Event == nil || saved.Messages[1].Focus == nil || len(saved.Messages[1].Focus.Topics) != len(ids) {
		t.Fatal("bulk subset failed canonical replay", w.Body.String())
	}
	if strings.Join(saved.Messages[0].Event.FocusIDs, ",") != strings.Join(ids, ",") {
		t.Fatal("persisted selection changed", saved.Messages[0].Event.FocusIDs)
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
				req.FolderContext.FocusIDs = make([]string, foldercontext.MaxFocusNodes+1)
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
	for _, count := range []int{foldercontext.MaxFocusNodes, foldercontext.MaxFocusNodes + 1} {
		ids := make([]string, count)
		for i := range ids {
			ids[i] = fmt.Sprintf("entry-%d", i)
		}
		encoded, err := json.Marshal(agenthttp.HomeAssistantFolderRef{SelectionID: "id", FocusIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		err = json.Unmarshal(encoded, &ref)
		if (err == nil) != (count == foldercontext.MaxFocusNodes) {
			t.Fatalf("reference count %d: %v", count, err)
		}
	}
}
