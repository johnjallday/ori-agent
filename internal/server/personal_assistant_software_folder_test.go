package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

// softwareFolderStage is a set of software fixtures moved into the sandboxed
// Documents folder and attached to a new conversation draft through the real
// folder-context route. candidates maps a fixture's folder name to the opaque
// candidate the preview disclosed for it.
type softwareFolderStage struct {
	draft      string
	selection  string
	candidates map[string]string
	dirs       map[string]string
}

// stageSoftwareFolders puts the named fixtures directly inside Documents. They
// are moved, not linked: the scan follows no link. A moved worktree's pointer no
// longer resolves, which nothing here reads.
func stageSoftwareFolders(t *testing.T, f *draftServerFixture, names ...string) softwareFolderStage {
	t.Helper()
	home := os.Getenv("HOME") // sandboxed by the fixture, never the user's home
	staging := filepath.Join(home, "Staging")
	documents := filepath.Join(home, "Documents")
	for _, dir := range []string{staging, documents} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	set, err := softwarefixture.Build(staging)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { softwarefixture.Restore(set.Unreadable) })
	stage := softwareFolderStage{draft: uuid.NewString(), candidates: map[string]string{}, dirs: map[string]string{}}
	for _, name := range names {
		target := filepath.Join(documents, name)
		if err := os.Rename(filepath.Join(set.Root, name), target); err != nil {
			t.Fatal(err)
		}
		stage.dirs[name] = target
	}
	stage.attach(t, f)
	return stage
}

// attach selects Documents for the stage's draft through the real route and
// records what the preview disclosed.
func (s *softwareFolderStage) attach(t *testing.T, f *draftServerFixture) {
	t.Helper()
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": s.draft, "revision": "", "mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, body)
	}
	observation := body["observation"].(map[string]any)
	s.selection = observation["id"].(string)
	for _, item := range observation["projects"].([]any) {
		project := item.(map[string]any)
		s.candidates[project["name"].(string)] = project["id"].(string)
	}
	for name := range s.dirs {
		if s.candidates[name] == "" {
			t.Fatalf("the preview did not disclose %s: %v", name, observation["projects"])
		}
	}
}

// reselect shows the same Documents folder to a new conversation draft.
func (s softwareFolderStage) reselect(t *testing.T, f *draftServerFixture) softwareFolderStage {
	t.Helper()
	fresh := softwareFolderStage{draft: uuid.NewString(), candidates: map[string]string{}, dirs: s.dirs}
	fresh.attach(t, f)
	return fresh
}

func (s softwareFolderStage) ask(prompt, conversationID, revision string) map[string]any {
	folder := map[string]any{"selection_id": s.selection}
	conversation := map[string]string{}
	if conversationID == "" {
		folder["draft_id"] = s.draft
	} else {
		conversation["id"], folder["revision"] = conversationID, revision
	}
	return map[string]any{
		"prompt": prompt, "intent": "assistant_conversation",
		"context": map[string]string{"origin": "personal_assistant_panel"}, "conversation": conversation,
		"folder_context": folder,
	}
}

func toolNames(request llm.ChatRequest) []string {
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func hasTool(request llm.ChatRequest, name string) bool {
	for _, tool := range request.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// softwareSetupState is everything a turn must leave alone: the workspaces, the
// pending folder offer, and each fixture's bytes.
type softwareSetupState struct {
	workspaces int
	offer      any
	hashes     map[string]string
}

func readSoftwareSetupState(t *testing.T, f *draftServerFixture, stage softwareFolderStage) softwareSetupState {
	t.Helper()
	_, digest := f.call(t, http.MethodGet, "/api/personal-assistant/folder-digest", nil)
	state := softwareSetupState{
		workspaces: len(f.builder.workspaceFileStore.CachedWorkspaces()),
		offer:      digest["folder_digest"].(map[string]any)["offer"], hashes: map[string]string{},
	}
	for name, dir := range stage.dirs {
		hash, err := softwarefixture.Hash(dir)
		if err != nil {
			t.Fatal(err)
		}
		state.hashes[name] = hash
	}
	return state
}

func (s softwareSetupState) assertUnchanged(t *testing.T, f *draftServerFixture, stage softwareFolderStage, when string) {
	t.Helper()
	now := readSoftwareSetupState(t, f, stage)
	if now.workspaces != s.workspaces {
		t.Fatalf("%s: workspaces went from %d to %d", when, s.workspaces, now.workspaces)
	}
	if now.offer != nil {
		t.Fatalf("%s: a folder offer appeared: %v", when, now.offer)
	}
	for name, hash := range s.hashes {
		if now.hashes[name] != hash {
			t.Fatalf("%s: the %s folder changed", when, name)
		}
	}
}

// Talking about a software folder, and even asking in words to set it up, is a
// conversation: one model call a turn, a suggestion to review, and nothing
// created. Loading the conversation again costs no model call at all.
func TestAssistantSoftwareFolder_DiscussionAndSetupWordingSuggestAndCreateNothing(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	stage := stageSoftwareFolders(t, f.draftServerFixture, "widget", "gauge")
	before := readSoftwareSetupState(t, f.draftServerFixture, stage)

	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", stage.ask("What is in this folder?", "", ""))
	if status != http.StatusOK || reply["folder_setup_suggestion"] == nil {
		t.Fatalf("discussion: %d %v", status, reply)
	}
	if len(f.provider.requests) != 1 {
		t.Fatalf("a discussion turn made %d model calls", len(f.provider.requests))
	}
	call := f.provider.requests[0]
	prompt := call.Messages[len(call.Messages)-1].Content
	for _, required := range []string{"<folder_setup_options>", "widget", "gauge", "Code project workspace"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the model was not told about %q: %s", required, prompt)
		}
	}
	// The model gets names and counts. It gets no path, no opaque reference, and
	// nothing read from inside the folders.
	for _, forbidden := range []string{os.Getenv("HOME"), stage.selection, stage.candidates["widget"], "example.invalid", "Widget is a small service", "never-read-this"} {
		if strings.Contains(prompt, forbidden) || strings.Contains(call.Messages[0].Content, forbidden) {
			t.Fatalf("the model was sent %q", forbidden)
		}
	}
	// A folder turn carries no workspace-proposal tool and no execution authority.
	if hasTool(call, "assistant_propose_workspace") {
		t.Fatalf("a folder turn offered the general workspace proposal: %v", toolNames(call))
	}
	assertNoPanelExecutionAuthority(t, call)
	before.assertUnchanged(t, f.draftServerFixture, stage, "after discussing the folder")

	conversation := reply["conversation"].(map[string]any)["id"].(string)
	revision := reply["folder_context"].(map[string]any)["revision"].(string)
	for _, wording := range []string{"Set this up as a workspace for me", "Yes, create the workspace now", "Go ahead and start building the login feature"} {
		status, reply = f.call(t, http.MethodPost, "/api/home-assistant/ask", stage.ask(wording, conversation, revision))
		if status != http.StatusOK || reply["requires_confirmation"] == true || reply["confirmation"] != nil {
			t.Fatalf("%q became an action: %d %v", wording, status, reply)
		}
		if reply["folder_setup_suggestion"] == nil {
			t.Fatalf("%q lost the review suggestion: %v", wording, reply)
		}
		revision = reply["folder_context"].(map[string]any)["revision"].(string)
		last := f.provider.requests[len(f.provider.requests)-1]
		if hasTool(last, "assistant_propose_workspace") {
			t.Fatalf("%q offered the general workspace proposal", wording)
		}
		before.assertUnchanged(t, f.draftServerFixture, stage, "after "+wording)
	}
	if len(f.provider.requests) != 4 {
		t.Fatalf("four turns made %d model calls", len(f.provider.requests))
	}

	// Reopening the conversation reads saved state: no model call, no offer.
	for range 3 {
		if status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil); status != http.StatusOK || history["folder_setup_suggestion"] == nil {
			t.Fatalf("hydration: %d %v", status, history)
		}
	}
	if len(f.provider.requests) != 4 {
		t.Fatalf("reopening the conversation made a model call (%d)", len(f.provider.requests))
	}
	before.assertUnchanged(t, f.draftServerFixture, stage, "after reopening the conversation")
}

// An explicit review of one software project makes one canonical offer for that
// project and no workspace, with no model call. Saying no to it removes that
// project from later suggestions without touching the other one.
func TestAssistantSoftwareFolder_ReviewIsOneOfferAndADeclineIsOnlyThatProjects(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	stage := stageSoftwareFolders(t, f.draftServerFixture, "widget", "gauge")
	before := readSoftwareSetupState(t, f.draftServerFixture, stage)
	review := map[string]any{"draft_id": stage.draft, "revision": "", "selection_id": stage.selection, "candidate_id": stage.candidates["widget"]}

	conversation, revision, offer := reviewFolder(t, f.draftServerFixture, review)
	if len(f.provider.requests) != 0 {
		t.Fatalf("a review made %d model calls", len(f.provider.requests))
	}
	if got := len(f.builder.workspaceFileStore.CachedWorkspaces()); got != before.workspaces {
		t.Fatalf("a review created a workspace (%d -> %d)", before.workspaces, got)
	}
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	software, ok := view["software"].(map[string]any)
	if !ok || software["recipe"] != "software_project" || software["marker"] != "Go module" || software["needs_candidate"] == true {
		t.Fatalf("the review is not about one recognized software project: %v", view)
	}
	if view["subject"].(map[string]any)["name"] != "widget" {
		t.Fatalf("the review is about %v", view["subject"])
	}
	// Reviewing the same candidate again is the same offer, not a second one.
	again := map[string]any{"conversation_id": conversation, "revision": revision, "selection_id": stage.selection, "candidate_id": stage.candidates["widget"]}
	if _, _, repeated := reviewFolder(t, f.draftServerFixture, again); repeated != offer {
		t.Fatalf("a repeated review made a second offer: %s then %s", offer, repeated)
	}

	status, declined := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "no", "request_id": "no-thanks"})
	if status != http.StatusOK {
		t.Fatalf("decline: %d %v", status, declined)
	}
	_, history = f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil)
	revision = history["folder_context"].(map[string]any)["revision"].(string)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", stage.ask("What else is here?", conversation, revision))
	if status != http.StatusOK || len(f.provider.requests) != 1 {
		t.Fatalf("turn after the decline: %d calls=%d %v", status, len(f.provider.requests), reply)
	}
	// This conversation still carries its declined review: the model is told
	// so, and is given no fresh setup option to offer in its place.
	prompt := f.provider.requests[0].Messages[len(f.provider.requests[0].Messages)-1].Content
	if !strings.Contains(prompt, `"status":"declined","subject":"widget"`) || reply["folder_setup_suggestion"] != nil {
		t.Fatalf("the declined review was not reported as declined: %v\n%s", reply["folder_setup_suggestion"], prompt)
	}
	// The declined project cannot be put back under review from an old control.
	revision = reply["folder_context"].(map[string]any)["revision"].(string)
	again["revision"] = revision
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", again); status != http.StatusConflict || body["error"] != "folder_review_declined" {
		t.Fatalf("a declined project was reviewed again: %d %v", status, body)
	}

	// Showing the same folder to a new conversation suggests the project that
	// was not declined, and only that one.
	fresh := stage.reselect(t, f.draftServerFixture)
	declinedAgain := map[string]any{"draft_id": fresh.draft, "revision": "", "selection_id": fresh.selection, "candidate_id": fresh.candidates["widget"]}
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", declinedAgain); status != http.StatusConflict || body["error"] != "folder_review_declined" {
		t.Fatalf("a declined project was reviewed from a new conversation: %d %v", status, body)
	}
	status, reply = f.call(t, http.MethodPost, "/api/home-assistant/ask", fresh.ask("What could I set up here?", "", ""))
	if status != http.StatusOK || len(f.provider.requests) != 2 || reply["folder_setup_suggestion"] == nil {
		t.Fatalf("a fresh look after the decline: %d calls=%d %v", status, len(f.provider.requests), reply)
	}
	prompt = f.provider.requests[1].Messages[len(f.provider.requests[1].Messages)-1].Content
	start := strings.Index(prompt, "<folder_setup_options>")
	options := prompt[start:strings.Index(prompt, "</folder_setup_options>")]
	if strings.Contains(options, "widget") || !strings.Contains(options, "gauge") {
		t.Fatalf("the declined project was suggested again, or the other one was dropped: %s", options)
	}
	if got := len(f.builder.workspaceFileStore.CachedWorkspaces()); got != before.workspaces {
		t.Fatalf("workspaces changed (%d -> %d)", before.workspaces, got)
	}
	for name, hash := range before.hashes {
		if now, err := softwarefixture.Hash(stage.dirs[name]); err != nil || now != hash {
			t.Fatalf("the %s folder changed: %v", name, err)
		}
	}
}

// The general workspace proposal belongs to ordinary turns. With no folder in
// the request it is offered, and accepting it is a pre-filled manual creator,
// never a folder offer and never access to a folder.
func TestAssistantSoftwareFolder_TheGeneralProposalStaysSeparateFromFolderSetup(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	stage := stageSoftwareFolders(t, f.draftServerFixture, "widget")
	before := readSoftwareSetupState(t, f.draftServerFixture, stage)

	f.provider.script = []llm.ChatResponse{readerCall("assistant_propose_workspace", map[string]any{
		"name": "Widget planning", "description": "Plan the next widget release. Nothing has been read or created yet.",
	})}
	reply := f.ask(t, "", "can we create a workspace to plan the widget release")
	if len(f.provider.requests) != 1 || !hasTool(f.provider.requests[0], "assistant_propose_workspace") {
		t.Fatalf("an ordinary turn did not offer the proposal in its one model call: %d %v", len(f.provider.requests), toolNames(f.provider.requests[0]))
	}
	confirmation, ok := reply["confirmation"].(map[string]any)
	if !ok || confirmation["action_type"] != "prepare_workspace" {
		t.Fatalf("no proposal: %v", reply)
	}
	if reply["folder_setup_suggestion"] != nil || reply["folder_context"] != nil {
		t.Fatalf("a proposal carried folder setup: %v", reply)
	}
	encoded := mustJSON(t, reply)
	for _, forbidden := range []string{os.Getenv("HOME"), stage.selection, stage.candidates["widget"], "folder_offer_id", "selection_id"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("the proposal carried %q", forbidden)
		}
	}
	before.assertUnchanged(t, f.draftServerFixture, stage, "after a general proposal")
}
