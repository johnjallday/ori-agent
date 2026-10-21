package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// No shared mutable recorder in ResolveRoot: concurrent proposal requests read
// the same held selection. Setup/observation recorders remain unused here.
type proposalFolders struct{ fakeBuildFolders }

func (f *proposalFolders) ResolveRoot(context.Context, foldercontext.Target, string) (string, error) {
	return "held-test-root", f.rootErr
}

func newProposalFixture(t *testing.T, replies ...string) (*buildFixture, personalassistant.WorkspaceBuildSession, *proposalFolders) {
	t.Helper()
	f := newBuildFixture(t, false, replies...)
	folders := &proposalFolders{}
	folders.observation = testObservation()
	f.handler.workspaceBuild.Folders = folders
	s := f.start(t)
	code, body := f.attachFolder(t, s, map[string]any{"mode": "picker"})
	if code != http.StatusOK {
		t.Fatalf("attach: %d %v", code, body)
	}
	return f, sessionOf(t, body), folders
}

func proposalRequest(s personalassistant.WorkspaceBuildSession, attempt int) map[string]any {
	return map[string]any{"intent": buildIntentSuggestFolder, "proposal_id": s.Proposal.ID, "attempt": attempt, "version": s.Version, "retry": s.Proposal.Status != "pending"}
}
func readProposalBuild(t *testing.T, f *buildFixture, id string) personalassistant.WorkspaceBuildSession {
	t.Helper()
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return *doc.Session(id)
}

func TestWorkspaceBuildProposalDraftOnlyAndReplay(t *testing.T) {
	malicious := strings.ReplaceAll(describeReply, `"create_now":false`, `"create_now":true`)
	f, s, folders := newProposalFixture(t, malicious)
	_, err := f.store.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		doc.Session(s.ID).TurnCount = 3
		doc.Session(s.ID).CreateNow = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeAgents := f.handler.agentStore.ListAgents()
	beforeWorkspaces, _ := f.handler.store.ListWorkspaces(context.Background())
	request := proposalRequest(s, 1)
	code, body := f.turn(t, s, request)
	got := sessionOf(t, body)
	if code != http.StatusOK || got.CreateNow || got.TurnCount != 3 || got.FirstRequest != "" || got.Retry != nil || got.Proposal.Attempt != 1 || got.Proposal.Status != "clarifying" {
		t.Fatalf("authority/provenance: %d %+v", code, got)
	}
	for _, entry := range got.Transcript {
		if entry.Role == personalassistant.BuildRoleUser {
			t.Fatal("fabricated user turn")
		}
	}
	if f.provider.calls != 1 || !strings.Contains(f.provider.systems[0], "NOT a user message") {
		t.Fatal("wrong prompt/budget")
	}
	code, body = f.turn(t, s, request) // lost-response replay uses original version
	if code != http.StatusOK || sessionOf(t, body).CreateNow || f.provider.calls != 1 {
		t.Fatalf("replay spent model call: %d %v", code, body)
	}
	afterWorkspaces, _ := f.handler.store.ListWorkspaces(context.Background())
	afterAgents := f.handler.agentStore.ListAgents()
	sort.Strings(beforeAgents)
	sort.Strings(afterAgents)
	if !reflect.DeepEqual(beforeAgents, afterAgents) || !reflect.DeepEqual(beforeWorkspaces, afterWorkspaces) || len(folders.started) != 0 || len(folders.reviewed) != 0 {
		t.Fatal("automatic operation had side effects")
	}
}

func TestWorkspaceBuildProposalRequestValidation(t *testing.T) {
	f, s, _ := newProposalFixture(t, describeReply)
	for _, change := range []map[string]any{
		{"text": "create it"}, {"auto": true}, {"choice_id": "try_again"}, {"intent": "create"},
		{"attempt": 0}, {"attempt": 4}, {"proposal_id": "/a/path"},
	} {
		req := proposalRequest(s, 1)
		for k, v := range change {
			req[k] = v
		}
		code, _ := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/turns", req)
		if code != http.StatusBadRequest {
			t.Fatalf("accepted %v: %d", change, code)
		}
	}
	for _, change := range []map[string]any{{"proposal_id": uuid.NewString()}, {"version": 999}, {"attempt": 2}} {
		req := proposalRequest(s, 1)
		for k, v := range change {
			req[k] = v
		}
		code, _ := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/turns", req)
		if code != http.StatusConflict {
			t.Fatalf("accepted stale %v: %d", change, code)
		}
	}
	if f.provider.calls != 0 {
		t.Fatal("invalid request spent provider budget")
	}
	f.handler.workspaceBuild.CurrentUserID = func(context.Context) (string, error) { return "", errors.New("identity unavailable") }
	code, _ := f.turn(t, s, proposalRequest(s, 1))
	if code != http.StatusConflict || f.provider.calls != 0 {
		t.Fatal("identity failure fell back to local")
	}
}

func TestWorkspaceBuildProposalFailedRetryIsBoundedAndNotUserRetry(t *testing.T) {
	f, s, _ := newProposalFixture(t)
	for attempt := 1; attempt <= personalassistant.BuildProposalMaxAttempts; attempt++ {
		original := s
		code, body := f.turn(t, s, proposalRequest(s, attempt))
		s = sessionOf(t, body)
		if code != http.StatusOK || s.Proposal.Status != "failed" || s.Retry != nil || s.CreateNow || s.TurnCount != 0 {
			t.Fatalf("failed %+v", s)
		}
		code, _ = f.turn(t, original, proposalRequest(original, attempt))
		if code != http.StatusOK || f.provider.calls != attempt {
			t.Fatal("duplicate failure spent another attempt")
		}
		code, _ = f.turn(t, s, map[string]any{"choice_id": "try_again"})
		if code != http.StatusConflict {
			t.Fatal("proposal failure became user retry")
		}
	}
}

func TestWorkspaceBuildProposalEarlierEmptyEditsAndTeamSurvive(t *testing.T) {
	f, s, _ := newProposalFixture(t, describeReply)
	draft := personalassistant.BuildDraft{Blank: true, TeamIntent: json.RawMessage(`{"mode":"agentless"}`), RoleStaffing: json.RawMessage(`[]`), ExistingAgentNames: []string{}}
	code, body := f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+s.ID+"/draft", map[string]any{
		"version": s.Version, "draft": draft, "edited_fields": []string{"name", "description", "goal", "team", "parent_id"},
	})
	if code != http.StatusOK {
		t.Fatal(body)
	}
	s = sessionOf(t, body)
	// Purpose change invalidates but deliberate Retry on the same folder is safe.
	code, body = f.turn(t, s, proposalRequest(s, 1))
	got := sessionOf(t, body)
	if code != http.StatusOK || got.Draft.Name != "" || got.Draft.Description != "" || !got.Draft.Blank || got.TeamPatch != nil || string(got.Draft.TeamIntent) != string(draft.TeamIntent) {
		t.Fatalf("overwrote ownership: %+v", got)
	}
	// A derived form sync must not acquire ownership of a new field.
	draft = got.Draft
	draft.Color = "#ef4444"
	_, body = f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+s.ID+"/draft", map[string]any{"version": got.Version, "draft": draft, "sync": true})
	if sessionOf(t, body).EditedFields["color"] != 0 {
		t.Fatal("sync marked a human edit")
	}
}

type barrierBuildProvider struct {
	*scriptedProvider
	started chan struct{}
	release chan struct{}
}

func (p *barrierBuildProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	close(p.started)
	<-p.release
	return p.next(req.SystemPrompt, req.Messages)
}

func TestWorkspaceBuildProposalPauseReportsNewerExplicitEdits(t *testing.T) {
	f, s, _ := newProposalFixture(t, describeReply)
	_, body := f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+s.ID+"/draft", map[string]any{"version": s.Version, "draft": s.Draft, "edited_fields": []string{"name"}})
	if sessionOf(t, body).Version <= s.Version {
		t.Fatal("edit not saved")
	}
	code, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/pause", map[string]any{"version": s.Version, "proposal_id": s.Proposal.ID, "context_version": s.ContextVersion, "attempt": 0})
	if code != http.StatusOK || body["paused"] != true || body["draft_changed"] != true {
		t.Fatalf("close hid a newer edit: %d %v", code, body)
	}
}

func TestWorkspaceBuildProposalAcceptedPreparationFencesLateReply(t *testing.T) {
	f := newBuildPreparationFixture(t)
	p := &barrierBuildProvider{scriptedProvider: &scriptedProvider{replies: []string{describeReply}}, started: make(chan struct{}), release: make(chan struct{})}
	defer close(p.release)
	f.h.workspaceBuild.Assistant = func(context.Context, string) (*personalassistant.Projection, error) {
		return &personalassistant.Projection{State: personalassistant.APIStateActive, GlobalAgentProfile: "Assistant", HQWorkspaceID: "synthetic-hq"}, nil
	}
	f.h.workspaceBuild.ResolveModel = func(context.Context, string) (WorkspaceBuildModel, error) {
		return WorkspaceBuildModel{Provider: p, Model: "test"}, nil
	}
	f.h.workspaceBuild.Catalog = func(context.Context, string) ([]WorkspaceBuildCatalogEntry, error) { return buildTestCatalog(), nil }
	doc, err := f.builds.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		b := doc.Session(f.build.ID)
		b.Proposal = &personalassistant.BuildProposal{ID: uuid.NewString(), FolderID: b.Folder.Observation.ID, Status: "pending"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.build = *doc.Session(f.build.ID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.call("turns", proposalRequest(f.build, 1)) }()
	select {
	case <-p.started:
	case result := <-done:
		t.Fatalf("proposal not started: %d %s", result.Code, result.Body.String())
	}
	doc, err = f.builds.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	f.build = *doc.Session(f.build.ID)
	review := f.review(t)
	prepared := f.call("janitor-prepare", review)
	if prepared.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", prepared.Code, prepared.Body.String())
	}
	// The accepted operation is real: creation, grant and initial metadata scan.
	before, _ := f.builds.Read(context.Background(), "local")
	accepted := *before.Session(f.build.ID)
	if accepted.Preparation == nil || accepted.Preparation.WorkspaceID == "" {
		t.Fatal("preparation not accepted")
	}
	p.release <- struct{}{}
	<-done
	after, _ := f.builds.Read(context.Background(), "local")
	got := after.Session(f.build.ID)
	if !reflect.DeepEqual(got.Draft, accepted.Draft) || !reflect.DeepEqual(got.Preparation, accepted.Preparation) || got.CreateNow || got.InferenceID != "" {
		t.Fatalf("late inference changed accepted preparation: %+v", got)
	}
	ids, _ := f.workspaces.List()
	if len(ids) != 1 || p.calls != 1 {
		t.Fatalf("workspaces=%v provider calls=%d", ids, p.calls)
	}
}

func TestWorkspaceBuildProposalConcurrentDuplicateAndInvalidation(t *testing.T) {
	for _, change := range []string{"none", "edit", "detach", "link", "purpose", "pause", "turn", "abandoned"} {
		t.Run(change, func(t *testing.T) {
			f, s, _ := newProposalFixture(t, describeReply)
			p := &barrierBuildProvider{scriptedProvider: f.provider, started: make(chan struct{}), release: make(chan struct{})}
			f.handler.workspaceBuild.ResolveModel = func(context.Context, string) (WorkspaceBuildModel, error) {
				return WorkspaceBuildModel{Provider: p, Model: "test"}, nil
			}
			done := make(chan map[string]any, 1)
			go func() { _, body := f.turn(t, s, proposalRequest(s, 1)); done <- body }()
			<-p.started
			// Same admission/version while running is observational.
			code, body := f.turn(t, s, proposalRequest(s, 1))
			if code != http.StatusOK || sessionOf(t, body).Proposal.Status != "running" {
				t.Fatalf("duplicate: %d %v", code, body)
			}
			current := readProposalBuild(t, f, s.ID)
			switch change {
			case "edit":
				_, body = f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+s.ID+"/draft", map[string]any{"version": current.Version, "draft": current.Draft, "edited_fields": []string{"name"}})
				if sessionOf(t, body).EditedFields["name"] == 0 {
					t.Fatal("empty edit not recorded")
				}
			case "detach":
				f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/folder/detach", map[string]any{"version": current.Version})
			case "pause":
				f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/pause", map[string]any{"version": s.Version, "proposal_id": s.Proposal.ID, "context_version": s.ContextVersion, "attempt": 1})
			case "purpose":
				draft := current.Draft
				draft.Blank = true
				f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+s.ID+"/draft", map[string]any{"version": current.Version, "draft": draft})
			case "link":
				f.handler.workspaceBuild.Links = &fakeBuildLinks{observation: testPageLink(true)}
				f.attachLink(t, current, "https://example.com/papers/attention")
			case "turn":
				// A second real turn uses a nonblocking provider while the old call waits.
				f.handler.workspaceBuild.ResolveModel = func(context.Context, string) (WorkspaceBuildModel, error) {
					return WorkspaceBuildModel{Provider: f.provider, Model: "test"}, nil
				}
				f.turn(t, current, map[string]any{"text": "use this for my notes"})
			case "abandoned":
				_, _ = f.store.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
					b := doc.Session(s.ID)
					b.InvalidateBuildInference(true)
					b.Status = personalassistant.WorkspaceBuildAbandoned
					b.Touch(f.store.Now())
					return nil
				})
			}
			close(p.release)
			response := <-done
			if change == "abandoned" {
				if response["code"] != "closed" {
					t.Fatal("abandoned lifecycle not fenced")
				}
				return
			}
			got := sessionOf(t, response)
			if got.CreateNow {
				t.Fatal("late reply revived create intent")
			}
			if change == "none" && got.Draft.Name != "Newsletter Desk" {
				t.Fatal("current suggestion not applied")
			}
			if change == "edit" && got.Draft.Name != "" {
				t.Fatal("clear lost")
			}
			if change != "none" && change != "edit" && change != "turn" && got.Draft.Name != "" {
				t.Fatal("stale reply applied")
			}
		})
	}
}
