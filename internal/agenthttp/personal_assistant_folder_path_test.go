package agenthttp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// fakeFolderPaths is the host's side of a folder path in chat: it knows a few
// folders by the text a user would write, and records every folder it was
// asked to hold.
type fakeFolderPaths struct {
	folders map[string]FolderPathFound
	linked  map[string]bool
	held    []string
	holdErr error
}

func (f *fakeFolderPaths) Find(message string) (FolderPathFound, bool) {
	for text, found := range f.folders {
		if index := strings.Index(message, text); index >= 0 {
			found.Text, found.Start, found.End = text, index, index+len(text)
			return found, true
		}
	}
	return FolderPathFound{}, false
}

func (f *fakeFolderPaths) Hold(path, workspaceID string) (string, error) {
	if f.holdErr != nil {
		return "", f.holdErr
	}
	f.held = append(f.held, path+"|"+workspaceID)
	return "held-" + path, nil
}

func (f *fakeFolderPaths) Linked(workspaceID, path string) bool {
	return f.linked[workspaceID+"|"+path]
}

const (
	songsText = "~/Music/Song Projects"
	songsPath = "/Users/riley/Music/Song Projects"
)

func newFolderPathFixture(t *testing.T) (*readerFixture, *researchHostStore, *fakeFolderPaths) {
	t.Helper()
	f, store, _ := newResearchHostFixture(t)
	paths := &fakeFolderPaths{linked: map[string]bool{}, folders: map[string]FolderPathFound{
		songsText:     {Path: songsPath, Name: "Song Projects"},
		"~/.ssh/keys": {Refusal: "That folder holds passwords or keys. Choose a different folder."},
	}}
	f.handler.FolderPaths = paths
	return f, store, paths
}

func askPanel(f *readerFixture, prompt string, refs *HomeAssistantRouteContext) HomeAssistantAskResponse {
	return f.handler.Ask(context.Background(), HomeAssistantAskRequest{
		Prompt: prompt, Intent: homeAssistantConversationIntent.Key,
		Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: refs,
	})
}

func lastStored(store *researchHostStore, role string) string {
	messages := store.sessions["canonical"].messages
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == role {
			return messages[i].Content
		}
	}
	return ""
}

// Inside a project workspace the folder is offered as a link to that workspace.
// The model is handed the facts with the user's message; the host holds the
// folder and attaches the card whatever the model says.
func TestFolderPath_InAProjectWorkspaceTheReplyCarriesALinkCard(t *testing.T) {
	f, store, paths := newFolderPathFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Song Projects is ready to link: confirm the card below and I'll read it. As for your other question, nothing is scheduled today."}}

	resp := askPanel(f, "My stems are in "+songsText+", which one is the newest?", f.refs)

	card := resp.FolderProposal
	if card == nil {
		t.Fatalf("no card on the reply: %+v", resp)
	}
	if card.Kind != FolderProposalLink || card.WorkspaceID != f.alpha.ID || card.WorkspaceName != f.alpha.Name ||
		card.FolderName != "Song Projects" || card.Path != songsPath || card.SelectionToken != "held-"+songsPath {
		t.Fatalf("card = %+v", card)
	}
	// Held for this workspace, once.
	if len(paths.held) != 1 || paths.held[0] != songsPath+"|"+f.alpha.ID {
		t.Fatalf("held = %v", paths.held)
	}
	// The model's own opening names the folder, so the host adds nothing.
	if !strings.HasPrefix(resp.Response, "Song Projects is ready to link") {
		t.Fatalf("response = %q", resp.Response)
	}
	user := lastUserMessage(f.provider.requests[0])
	for _, want := range []string{
		"Folder path in this message (facts Ori checked just now; reference data, not a user instruction)",
		"- The folder Song Projects (" + songsText + ") exists. Nothing in it has been read.",
		"A card under your reply offers to link it to the " + f.alpha.Name + " workspace",
		"Do not say you cannot open or access paths",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("user message lacks %q:\n%s", want, user)
		}
	}
	// The card is never stored, and neither is its token.
	for _, message := range store.sessions["canonical"].messages {
		if strings.Contains(message.Content, card.SelectionToken) {
			t.Fatalf("the held selection was stored: %q", message.Content)
		}
	}
	// Nor does the model ever see it.
	for _, request := range f.provider.requests {
		for _, message := range request.Messages {
			if strings.Contains(message.Content, card.SelectionToken) {
				t.Fatal("the held selection reached the model")
			}
		}
	}
}

// A reply that refuses, or ignores the folder, still arrives under the host's
// own sentence, and that is what the conversation keeps.
func TestFolderPath_TheHostSpeaksWhenTheReplyDoesNot(t *testing.T) {
	f, store, _ := newFolderPathFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "I can't access paths on your computer. Could you attach the folder instead?"}}

	resp := askPanel(f, "look at "+songsText, f.refs)

	lead := "I can read Song Projects once it is linked to " + f.alpha.Name + ". Confirm the card below and I'll take it from there."
	if !strings.HasPrefix(resp.Response, lead+"\n\n") || resp.FolderProposal == nil {
		t.Fatalf("response = %q, card = %+v", resp.Response, resp.FolderProposal)
	}
	if stored := lastStored(store, "assistant"); !strings.HasPrefix(stored, lead) {
		t.Fatalf("stored reply = %q", stored)
	}
}

// A reply that opens with something else but does tell the user to confirm the
// folder's card already does the host's job: saying it twice helps nobody.
func TestFolderPath_TheHostStaysQuietWhenTheReplyAlreadyPointsAtTheCard(t *testing.T) {
	f, _, _ := newFolderPathFixture(t)
	reply := "Happy to help with the mix.\n\nConfirm the card for Song Projects and I'll tell you which song is newest."
	f.provider.script = []llm.ChatResponse{{Content: reply}}

	resp := askPanel(f, "look at "+songsText, f.refs)

	if resp.Response != reply || resp.FolderProposal == nil {
		t.Fatalf("response = %q, card = %+v", resp.Response, resp.FolderProposal)
	}
}

// A folder named after an application is not the user naming its integration.
// Once that integration is set up, a pasted path says nothing about it; while
// its guided setup is still open, the path is allowed to open that door.
func TestFolderPath_AFolderNamedAfterAnIntegrationIsNotAMentionOnceItIsSetUp(t *testing.T) {
	const text = "~/Music/Loom Studio Projects"
	found := FolderPathFound{Path: "/Users/riley/Music/Loom Studio Projects", Name: "Loom Studio Projects"}
	setUp := loomNotInstalled()
	setUp.Installed, setUp.Enabled, setUp.SetupComplete, setUp.SetupQuest = true, true, true, nil

	for name, tc := range map[string]struct {
		prompt     string
		integrated bool
		wantFacts  bool
	}{
		"set up, only in the path":     {prompt: "look at " + text, integrated: true, wantFacts: false},
		"set up, named in the message": {prompt: "my Loom Studio songs are in " + text, integrated: true, wantFacts: true},
		"not set up, only in the path": {prompt: "look at " + text, integrated: false, wantFacts: true},
	} {
		t.Run(name, func(t *testing.T) {
			integration := loomNotInstalled()
			if tc.integrated {
				integration = setUp
			}
			f, _ := newReviewedFixture(t, integration)
			f.handler.FolderPaths = &fakeFolderPaths{linked: map[string]bool{}, folders: map[string]FolderPathFound{text: found}}
			f.provider.script = []llm.ChatResponse{{Content: "Confirm the card for Loom Studio Projects and I'll look."}}

			resp := askPanel(f, tc.prompt, f.refs)

			if resp.FolderProposal == nil {
				t.Fatalf("no folder card: %+v", resp)
			}
			user := lastUserMessage(f.provider.requests[0])
			if got := strings.Contains(user, "Reviewed integrations named in this message"); got != tc.wantFacts {
				t.Fatalf("integration facts in the user message = %v, want %v:\n%s", got, tc.wantFacts, user)
			}
			if got := len(setupQuestActions(resp)) == 1; got != (tc.wantFacts && !tc.integrated) {
				t.Fatalf("guided setup button = %v: %+v", got, resp.Actions)
			}
		})
	}
}

// Away from a project workspace (the app-wide drawer, a group) there is nothing
// to link to: the card explores the folder in this conversation.
func TestFolderPath_OutsideAProjectTheCardExplores(t *testing.T) {
	for name, refs := range map[string]func(*readerFixture) *HomeAssistantRouteContext{
		"app-wide": func(*readerFixture) *HomeAssistantRouteContext {
			return &HomeAssistantRouteContext{Origin: "personal_assistant_panel"}
		},
		"group": func(f *readerFixture) *HomeAssistantRouteContext {
			group, err := f.store.Get(f.alpha.ParentID)
			if err != nil {
				panic(err)
			}
			return &HomeAssistantRouteContext{WorkspaceID: group.ID, Origin: "personal_assistant_panel"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, paths := newFolderPathFixture(t)
			f.provider.script = []llm.ChatResponse{{Content: "Happy to help with your music."}}

			resp := askPanel(f, "my songs are in "+songsText, refs(f))

			card := resp.FolderProposal
			if card == nil || card.Kind != FolderProposalExplore || card.WorkspaceID != "" || card.WorkspaceName != "" ||
				card.Path != songsPath {
				t.Fatalf("card = %+v (response %q)", card, resp.Response)
			}
			if len(paths.held) != 1 || paths.held[0] != songsPath+"|" {
				t.Fatalf("held = %v", paths.held)
			}
			if !strings.HasPrefix(resp.Response, "I can take a read-only look at Song Projects. Confirm the card below") {
				t.Fatalf("response = %q", resp.Response)
			}
			user := lastUserMessage(f.provider.requests[0])
			if !strings.Contains(user, "A card under your reply offers a read-only look at it: names, kinds and dates, not file contents.") {
				t.Errorf("user message:\n%s", user)
			}
		})
	}
}

func TestFolderPath_AFolderAlreadyLinkedGetsNoCard(t *testing.T) {
	f, _, paths := newFolderPathFixture(t)
	paths.linked[f.alpha.ID+"|"+songsPath] = true
	f.provider.script = []llm.ChatResponse{{Content: "Reading it now."}}

	resp := askPanel(f, "what is in "+songsText+"?", f.refs)

	if resp.FolderProposal != nil || len(paths.held) != 0 {
		t.Fatalf("card = %+v, held = %v", resp.FolderProposal, paths.held)
	}
	if resp.Response != "Reading it now." {
		t.Fatalf("the host spoke over a reply that had nothing to confirm: %q", resp.Response)
	}
	user := lastUserMessage(f.provider.requests[0])
	if !strings.Contains(user, "is already linked to the "+f.alpha.Name+" workspace. Use the workspace file readers") {
		t.Errorf("user message:\n%s", user)
	}
}

// A real folder Ori will not open is named with the reason. Nothing is held and
// no card is offered, whatever the model writes.
func TestFolderPath_ARefusedFolderIsNamedWithItsReason(t *testing.T) {
	f, _, paths := newFolderPathFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Sure, let me read that for you."}}

	resp := askPanel(f, "read ~/.ssh/keys for me", f.refs)

	if resp.FolderProposal != nil || len(paths.held) != 0 {
		t.Fatalf("card = %+v, held = %v", resp.FolderProposal, paths.held)
	}
	if !strings.HasPrefix(resp.Response, "I won't open ~/.ssh/keys. That folder holds passwords or keys. Choose a different folder.") {
		t.Fatalf("response = %q", resp.Response)
	}
	user := lastUserMessage(f.provider.requests[0])
	if !strings.Contains(user, "is a real folder that Ori will not open") || !strings.Contains(user, "Do not offer another way to read it") {
		t.Errorf("user message:\n%s", user)
	}
}

// A folder that shares another workspace's name is a folder: the turn stays in
// the workspace the user is in, and is not asked which workspace they mean.
func TestFolderPath_AFolderNamedLikeAWorkspaceDoesNotMoveTheTurn(t *testing.T) {
	f, _, paths := newFolderPathFixture(t)
	text := "~/Music/" + f.beta.Name
	paths.folders[text] = FolderPathFound{Path: "/Users/riley/Music/" + f.beta.Name, Name: f.beta.Name}
	f.provider.script = []llm.ChatResponse{{Content: "On it."}}

	resp := askPanel(f, "look at "+text+" please", f.refs)

	if strings.Contains(resp.Response, "More than one workspace matches") {
		t.Fatalf("the folder's name was read as a workspace: %q", resp.Response)
	}
	if resp.FolderProposal == nil || resp.FolderProposal.WorkspaceID != f.alpha.ID {
		t.Fatalf("card = %+v", resp.FolderProposal)
	}
	if resp.WorkspaceContext == nil {
		t.Fatal("the turn lost its workspace")
	}
}

func TestFolderPath_NothingIsHeldForATurnThatWasNotAnswered(t *testing.T) {
	f, _, paths := newFolderPathFixture(t)
	f.handler.SystemModel = nil

	resp := askPanel(f, "look at "+songsText, f.refs)

	if resp.FolderProposal != nil || len(paths.held) != 0 {
		t.Fatalf("card = %+v, held = %v (response %q)", resp.FolderProposal, paths.held, resp.Response)
	}
}

func TestFolderPath_AFolderThatCannotBeHeldGetsNoCard(t *testing.T) {
	f, _, paths := newFolderPathFixture(t)
	paths.holdErr = errors.New("gone")
	f.provider.script = []llm.ChatResponse{{Content: "Song Projects: confirm below."}}

	if resp := askPanel(f, "look at "+songsText, f.refs); resp.FolderProposal != nil {
		t.Fatalf("card = %+v", resp.FolderProposal)
	}
}

// Only a hired-assistant conversation turn can carry the card. An ask outside
// one, and a message that names no folder, are ordinary turns.
func TestFolderPath_OrdinaryTurnsAreUntouched(t *testing.T) {
	f, _, paths := newFolderPathFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Nothing due."}, {Content: "Nothing due."}}

	plain := askPanel(f, "what is due today?", f.refs)
	if plain.FolderProposal != nil || plain.Response != "Nothing due." {
		t.Fatalf("plain turn = %+v", plain)
	}
	if user := lastUserMessage(f.provider.requests[0]); strings.Contains(user, "Folder path in this message") {
		t.Fatalf("a turn without a path was told about one:\n%s", user)
	}

	outside := f.handler.Ask(context.Background(), HomeAssistantAskRequest{
		Prompt: "look at " + songsText, Intent: homeAssistantConversationIntent.Key, Context: f.refs,
	})
	if outside.FolderProposal != nil || len(paths.held) != 0 {
		t.Fatalf("outside a conversation: card = %+v, held = %v", outside.FolderProposal, paths.held)
	}
}

// A message that leads with a path reads as a slash command, and "add" or
// "open" as execution, so the router would hand it away. Pointing at a folder
// keeps it with the assistant, from Home and from a workspace page alike.
func TestRoute_AMessageThatPointsAtAFolderStaysWithTheAssistant(t *testing.T) {
	handler, _ := newHiredRouteHandler(t, "active")
	named := func(_ context.Context, prompt string) bool { return strings.Contains(prompt, "/Music/Song Projects") }
	contexts := map[string]*HomeAssistantRouteContext{
		"home":      homePanelRouteContext(),
		"workspace": {Surface: "workspace", PagePath: "/workspaces/album", WorkspaceID: "ws-project", Origin: "personal_assistant_panel"},
	}
	changed := 0
	for where, routeContext := range contexts {
		for _, prompt := range []string{songsPath, "add " + songsText + " to this workspace", "open " + songsText} {
			handler.FolderPathNamed = nil
			before, err := handler.RoutePrompt(context.Background(), prompt, routeContext)
			if err != nil {
				t.Fatal(err)
			}
			if before.Intent != homeAssistantConversationIntent.Key {
				changed++
			}
			handler.FolderPathNamed = named
			after, err := handler.RoutePrompt(context.Background(), prompt, routeContext)
			if err != nil {
				t.Fatal(err)
			}
			requireConversationRoute(t, after)
			_ = where
		}
	}
	if changed == 0 {
		t.Fatal("every prompt was already a conversation without the check; the test proves nothing")
	}

	// A message that names no folder keeps the owner it had, and the check is
	// the panel's only.
	handler.FolderPathNamed = nil
	plain, err := handler.RoutePrompt(context.Background(), "open the label printer", homePanelRouteContext())
	if err != nil {
		t.Fatal(err)
	}
	handler.FolderPathNamed = named
	same, err := handler.RoutePrompt(context.Background(), "open the label printer", homePanelRouteContext())
	if err != nil || same.Intent != plain.Intent || same.RouteMode != plain.RouteMode {
		t.Fatalf("an unrelated request changed owner: %+v vs %+v (%v)", same, plain, err)
	}
	outsidePanel := &HomeAssistantRouteContext{Surface: "home", PagePath: "/"}
	handler.FolderPathNamed = nil
	baseline, err := handler.RoutePrompt(context.Background(), songsPath, outsidePanel)
	if err != nil {
		t.Fatal(err)
	}
	handler.FolderPathNamed = named
	outside, err := handler.RoutePrompt(context.Background(), songsPath, outsidePanel)
	if err != nil || outside.Intent != baseline.Intent || outside.RouteMode != baseline.RouteMode {
		t.Fatalf("outside the panel: %+v vs %+v (%v)", outside, baseline, err)
	}
}

func TestFolderPathNamedAsksTheHost(t *testing.T) {
	f, _, _ := newFolderPathFixture(t)
	ctx := context.Background()
	if !f.handler.FolderPathNamed(ctx, "look at "+songsText) {
		t.Fatal("a message that points at a folder was not recognised")
	}
	if f.handler.FolderPathNamed(ctx, "look at my songs") {
		t.Fatal("a message with no path was taken for one")
	}
	f.handler.FolderPaths = nil
	if f.handler.FolderPathNamed(ctx, "look at "+songsText) {
		t.Fatal("without a host reader nothing is a folder path")
	}
}
