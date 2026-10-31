package agenthttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// fakeCapturer records what reached the second-brain service.
type fakeCapturer struct {
	available bool
	err       error
	calls     []fakeCapture
	projects  []personalassistant.SecondBrainProjectView
}

type fakeCapture struct{ userID, text, requestID string }

func (f *fakeCapturer) CaptureAvailable(context.Context, string) bool { return f.available }

func (f *fakeCapturer) Projects(context.Context, string) []personalassistant.SecondBrainProjectView {
	return f.projects
}

func (f *fakeCapturer) Capture(_ context.Context, userID, text, requestID string) (personalassistant.SecondBrainCaptureView, error) {
	f.calls = append(f.calls, fakeCapture{userID, text, requestID})
	if f.err != nil {
		return personalassistant.SecondBrainCaptureView{}, f.err
	}
	return personalassistant.SecondBrainCaptureView{
		Note: "Inbox/20261031-1407-call-dana.md", Name: "20261031-1407-call-dana",
		Link: "obsidian://open?path=%2FUsers%2Fsam%2FSecond%20Brain%2FInbox%2F20261031-1407-call-dana.md",
	}, nil
}

func captureCall(arguments string) llm.ChatResponse {
	return llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "call-1", Name: secondBrainCaptureTool, Arguments: arguments}}}
}

func newCaptureFixture(t *testing.T, capturer *fakeCapturer) *readerFixture {
	t.Helper()
	f, _, _ := newResearchHostFixture(t)
	f.handler.HireProposals = nil
	f.handler.SetSecondBrainCapture(capturer)
	return f
}

func TestSecondBrainCaptureIsOfferedOnlyWhileASecondBrainOriSetUpIsConnected(t *testing.T) {
	for name, tc := range map[string]struct {
		capturer *fakeCapturer
		offered  bool
	}{
		"a second brain Ori set up": {&fakeCapturer{available: true}, true},
		"a connected vault":         {&fakeCapturer{available: false}, false},
		"no notes service":          {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := newResearchHostFixture(t)
			f.handler.HireProposals = nil
			if tc.capturer != nil {
				f.handler.SetSecondBrainCapture(tc.capturer)
			}
			f.provider.script = []llm.ChatResponse{{Content: "Hello."}}
			askHire(f, "what is on my plate today")
			request := f.provider.requests[0]
			offered := strings.Contains(strings.Join(toolNamesOf(request), ","), secondBrainCaptureTool)
			if offered != tc.offered {
				t.Fatalf("tool offered = %v, want %v", offered, tc.offered)
			}
			if instructed := strings.Contains(systemText(request), "second_brain_capture saves one note"); instructed != tc.offered {
				t.Fatalf("instructions present = %v, want %v", instructed, tc.offered)
			}
		})
	}
}

func TestSecondBrainCaptureSavesOneNoteForTheTurnAndNamesItUnderTheReply(t *testing.T) {
	capturer := &fakeCapturer{available: true}
	f := newCaptureFixture(t, capturer)
	f.provider.script = []llm.ChatResponse{
		captureCall(`{"text":"Call Dana about the pressing run"}`),
		{Content: "Done, it's in your Inbox."},
	}
	response := askHire(f, "note this: call Dana about the pressing run")

	if len(capturer.calls) != 1 || capturer.calls[0].text != "Call Dana about the pressing run" || capturer.calls[0].userID != "local" ||
		!strings.HasPrefix(capturer.calls[0].requestID, "sbcap-") {
		t.Fatalf("calls = %+v", capturer.calls)
	}
	want := "Done, it's in your Inbox.\n\nSaved in your second brain: Inbox/20261031-1407-call-dana.md\nobsidian://open?path="
	if !strings.HasPrefix(response.Response, want) {
		t.Fatalf("response = %q", response.Response)
	}
	if response.Confirmation != nil || response.RequiresConfirmation {
		t.Fatalf("a note acquired an action card: %+v", response)
	}
	// The result the model saw says where the note went, and nothing of the
	// request id or the path on disk.
	last := f.provider.requests[len(f.provider.requests)-1]
	serialized, _ := json.Marshal(last.Messages)
	if !strings.Contains(string(serialized), "Saved as Inbox/20261031-1407-call-dana.md") {
		t.Fatalf("the model was not told the note was saved: %s", serialized)
	}
}

func TestSecondBrainCaptureTakesOneNotePerTurnAndOneRequestID(t *testing.T) {
	capturer := &fakeCapturer{available: true}
	f := newCaptureFixture(t, capturer)
	f.provider.script = []llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{
			{ID: "a", Name: secondBrainCaptureTool, Arguments: `{"text":"one"}`},
			{ID: "b", Name: secondBrainCaptureTool, Arguments: `{"text":"two"}`},
		}},
		{Content: "Saved the first."},
	}
	response := askHire(f, "note this: one. and two.")
	if len(capturer.calls) != 1 || capturer.calls[0].text != "one" {
		t.Fatalf("calls = %+v; want only the first note", capturer.calls)
	}
	if strings.Count(response.Response, "Saved in your second brain:") != 1 {
		t.Fatalf("response = %q", response.Response)
	}
	last := f.provider.requests[len(f.provider.requests)-1]
	serialized, _ := json.Marshal(last.Messages)
	if !strings.Contains(string(serialized), "one note was already saved this turn") {
		t.Fatalf("the model was not told the second note was refused: %s", serialized)
	}
}

func TestSecondBrainCaptureRefusalsSaveNothingAndAddNoSavedLine(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		arguments string
		called    bool
		told      string
	}{
		"a secret":          {secondbrain.ErrCaptureSecretLike, `{"text":"key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"}`, true, "looks like a secret"},
		"an empty note":     {secondbrain.ErrCaptureInvalid, `{"text":" "}`, true, "1 to 4,000 characters"},
		"not set up by Ori": {secondbrain.ErrNotOwned, `{"text":"Call Dana"}`, true, "no second brain set up by Ori"},
		"a write failure":   {context.DeadlineExceeded, `{"text":"Call Dana"}`, true, "could not be written"},
		"an unknown field":  {nil, `{"text":"Call Dana","folder":"Projects"}`, false, ""},
		"not an object":     {nil, `"Call Dana"`, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			capturer := &fakeCapturer{available: true, err: tc.err}
			f := newCaptureFixture(t, capturer)
			f.provider.script = []llm.ChatResponse{captureCall(tc.arguments), {Content: "I couldn't save that."}}
			response := askHire(f, "remember this in my notes")
			if (len(capturer.calls) == 1) != tc.called {
				t.Fatalf("calls = %+v, want called = %v", capturer.calls, tc.called)
			}
			if strings.Contains(response.Response, "Saved in your second brain") {
				t.Fatalf("a refusal claims a save: %q", response.Response)
			}
			last := f.provider.requests[len(f.provider.requests)-1]
			serialized, _ := json.Marshal(last.Messages)
			if tc.told != "" && !strings.Contains(string(serialized), tc.told) {
				t.Fatalf("the model was not told %q: %s", tc.told, serialized)
			}
			if strings.Contains(string(serialized), "sk-ant") && tc.err == secondbrain.ErrCaptureSecretLike {
				// The secret is in the model's own call, never in what comes back.
				if strings.Count(string(serialized), "sk-ant") > 1 {
					t.Fatalf("the refusal repeats the secret: %s", serialized)
				}
			}
		})
	}
}

func TestSecondBrainWorkspaceButtonOpensTheFormOnTheNamedProjectAndCreatesNothing(t *testing.T) {
	projects := []personalassistant.SecondBrainProjectView{
		{Name: "Launch", Rel: "Projects/Launch", Path: "/Users/sam/Second Brain/Projects/Launch"},
		{Name: "Album 2: remix", Rel: "Projects/Album 2: remix", Path: "/Users/sam/Second Brain/Projects/Album 2: remix"},
		{Name: "Zine", Rel: "Projects/Zine", Path: "/Users/sam/Second Brain/Projects/Zine"},
	}
	for name, tc := range map[string]struct {
		prompt string
		want   []string // labels
	}{
		"a named project":                {"make Launch a workspace", []string{"Make Launch a workspace"}},
		"case does not matter":           {"Set up a WORKSPACE for launch please", []string{"Make Launch a workspace"}},
		"agents for it":                  {"set up agents for Zine", []string{"Make Zine a workspace"}},
		"two projects":                   {"a workspace each for launch and zine", []string{"Make Launch a workspace", "Make Zine a workspace"}},
		"an odd name":                    {"make album 2: remix a workspace", []string{"Make Album 2: remix a workspace"}},
		"no project named":               {"make this a workspace", nil},
		"a project named but no ask":     {"how is Launch going", nil},
		"a project that is not in notes": {"make Podcast a workspace", nil},
	} {
		t.Run(name, func(t *testing.T) {
			capturer := &fakeCapturer{available: true, projects: projects}
			f := newCaptureFixture(t, capturer)
			f.provider.script = []llm.ChatResponse{{Content: "Sure."}}
			response := askHire(f, tc.prompt)
			var labels []string
			for _, action := range response.Actions {
				if strings.HasPrefix(action.ID, "make-workspace-") {
					labels = append(labels, action.Label)
					if action.Type != HomeActionNavigate || !strings.HasPrefix(action.Href, "/?create=1&second_brain_project=") ||
						strings.Contains(action.Href, "Users") || strings.Contains(action.Href, "%2FUsers") {
						t.Errorf("action = %+v; want a navigation by name, no path", action)
					}
				}
			}
			if strings.Join(labels, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("buttons = %v, want %v", labels, tc.want)
			}
			if len(capturer.calls) != 0 || response.RequiresConfirmation || response.Confirmation != nil {
				t.Fatalf("the reply created or confirmed something: %+v / %+v", capturer.calls, response)
			}
		})
	}

	t.Run("none for a connected vault or without the notes service", func(t *testing.T) {
		vault := &fakeCapturer{available: false, projects: projects}
		f := newCaptureFixture(t, vault)
		f.provider.script = []llm.ChatResponse{{Content: "Sure."}}
		for _, action := range askHire(f, "make Launch a workspace").Actions {
			if strings.HasPrefix(action.ID, "make-workspace-") {
				t.Fatalf("a connected vault got %+v", action)
			}
		}
		f, _, _ = newResearchHostFixture(t)
		f.handler.HireProposals = nil
		f.provider.script = []llm.ChatResponse{{Content: "Sure."}}
		for _, action := range askHire(f, "make Launch a workspace").Actions {
			if strings.HasPrefix(action.ID, "make-workspace-") {
				t.Fatalf("no notes service, yet %+v", action)
			}
		}
	})

	t.Run("the link is built from the name alone", func(t *testing.T) {
		if got := secondBrainProjectHref("Album 2: remix"); got != "/?create=1&second_brain_project=Album+2%3A+remix" {
			t.Fatalf("href = %q", got)
		}
	})

	t.Run("the instructions say a button appears and nothing is created", func(t *testing.T) {
		if !strings.Contains(secondBrainCaptureInstructions, "create nothing and never say you did") ||
			!strings.Contains(secondBrainCaptureInstructions, "if they have not named a project, ask which") {
			t.Fatal("the workspace rules are missing from the capture instructions")
		}
	})
}

func TestSecondBrainCaptureTurnIsNilOutsideTheAssistantConversation(t *testing.T) {
	capturer := &fakeCapturer{available: true}
	f := newCaptureFixture(t, capturer)
	if turn := f.handler.secondBrainCaptureTurn(context.Background(), false); turn != nil {
		t.Fatal("a turn that is not the assistant's own conversation got the tool")
	}
	if turn := f.handler.secondBrainCaptureTurn(context.Background(), true); turn == nil || turn.userID != "local" {
		t.Fatalf("turn = %+v", turn)
	}
	var none *secondBrainCaptureTurn
	if got := none.appendSaved("hello"); got != "hello" {
		t.Fatalf("a nil turn changed the reply: %q", got)
	}
}
