package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/linkcontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestWorkspaceBuildProposalProviderBudgetAndSelection(t *testing.T) {
	for _, scenario := range []string{"repair", "expired", "recognized", "collection"} {
		t.Run(scenario, func(t *testing.T) {
			createOnly := strings.ReplaceAll(talkOnlyReply, `"create_now":false`, `"create_now":true`)
			f, s, folders := newProposalFixture(t, createOnly, createOnly)
			if scenario == "expired" {
				folders.rootErr = personalassistant.ErrFolderSelection
			}
			if scenario == "recognized" || scenario == "collection" {
				_, _ = f.store.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
					b := doc.Session(s.ID)
					b.Folder.Recognition = reaperRecognition()
					if scenario == "collection" {
						b.Folder.Recognition.Verdict = personalassistant.BuildFolderCollection
					}
					return nil
				})
			}
			code, body := f.turn(t, s, proposalRequest(s, 1))
			if scenario != "repair" {
				if code != http.StatusConflict || f.provider.calls != 0 {
					t.Fatal("selection/plan authority bypassed")
				}
				return
			}
			got := sessionOf(t, body)
			if got.Proposal.Attempt != 1 || got.Proposal.Status != "clarifying" || got.CreateNow || f.provider.calls != 2 {
				t.Fatalf("logical/provider budget: %+v calls=%d", got, f.provider.calls)
			}
			f.turn(t, s, proposalRequest(s, 1))
			if f.provider.calls != 2 {
				t.Fatal("replay repeated parse/repair budget")
			}
		})
	}
}

func TestWorkspaceBuildProposalInvalidationRequiresDeliberateRetry(t *testing.T) {
	f, s, _ := newProposalFixture(t, describeReply)
	f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/pause", map[string]any{"version": s.Version})
	paused := readProposalBuild(t, f, s.ID)
	req := proposalRequest(paused, 1)
	req["retry"] = false
	code, _ := f.turn(t, paused, req)
	if code != http.StatusConflict || f.provider.calls != 0 {
		t.Fatal("paused activity auto-restarted")
	}
	code, body := f.turn(t, paused, proposalRequest(paused, 1))
	if code != http.StatusOK || sessionOf(t, body).Proposal.Attempt != 1 {
		t.Fatal("deliberate retry unavailable")
	}
}

func TestWorkspaceBuildProposalLinkAndFolderShareOnePendingSnapshot(t *testing.T) {
	for _, order := range []string{"link-first", "folder-first"} {
		t.Run(order, func(t *testing.T) {
			f := newBuildFixture(t, false, describeReply)
			f.handler.workspaceBuild.Folders = &proposalFolders{fakeBuildFolders: fakeBuildFolders{observation: testObservation()}}
			f.handler.workspaceBuild.Links = &fakeBuildLinks{observation: testPageLink(true)}
			s := f.start(t)
			var body map[string]any
			if order == "link-first" {
				_, body = f.attachLink(t, s, "https://example.com/papers/attention")
				s = sessionOf(t, body)
				if s.Proposal != nil {
					t.Fatal("link-only started suggestion")
				}
				_, body = f.attachFolder(t, s, map[string]any{"mode": "picker"})
			} else {
				_, body = f.attachFolder(t, s, map[string]any{"mode": "picker"})
				s = sessionOf(t, body)
				proposalID := s.Proposal.ID
				_, body = f.attachLink(t, s, "https://example.com/papers/attention")
				if sessionOf(t, body).Proposal.ID != proposalID {
					t.Fatal("link issued a second logical proposal")
				}
			}
			s = sessionOf(t, body)
			_, body = f.turn(t, s, proposalRequest(s, 1))
			if sessionOf(t, body).Link == nil || f.provider.calls != 1 || !strings.Contains(f.provider.systems[0], "<link_observation>") || !strings.Contains(f.provider.systems[0], "<folder_observation>") {
				t.Fatal("incomplete combined snapshot")
			}
		})
	}
}

type barrierBuildLinks struct{ started, release chan struct{} }

func (b *barrierBuildLinks) Observe(context.Context, string) (*linkcontext.Observation, error) {
	close(b.started)
	<-b.release
	return testPageLink(true), nil
}

type barrierBuildFolders struct {
	proposalFolders
	started, release chan struct{}
}

func (b *barrierBuildFolders) Observe(context.Context, foldercontext.Target, string, string) (*foldercontext.Observation, error) {
	close(b.started)
	<-b.release
	return testObservation(), nil
}

func TestWorkspaceBuildProposalLateLinkWaitsForFirstAdmission(t *testing.T) {
	f, s, _ := newProposalFixture(t, describeReply)
	links := &barrierBuildLinks{started: make(chan struct{}), release: make(chan struct{})}
	f.handler.workspaceBuild.Links = links
	done := make(chan map[string]any, 1)
	go func() { _, body := f.attachLink(t, s, "https://example.com/papers/attention"); done <- body }()
	<-links.started
	current := readProposalBuild(t, f, s.ID)
	code, _ := f.turn(t, current, proposalRequest(current, 1))
	if code != http.StatusConflict || f.provider.calls != 0 {
		t.Fatal("inference raced the unfinished link")
	}
	close(links.release)
	current = sessionOf(t, <-done)
	if current.Proposal.ID != s.Proposal.ID || current.Proposal.Status != "pending" {
		t.Fatal("late link changed logical identity")
	}
	code, body := f.turn(t, current, proposalRequest(current, 1))
	if code != http.StatusOK || sessionOf(t, body).Link == nil || f.provider.calls != 1 {
		t.Fatal("combined snapshot not admitted")
	}
}

func TestWorkspaceBuildProposalLateAttachmentCannotSurvivePauseOrDetach(t *testing.T) {
	for _, kind := range []string{"folder", "link"} {
		for _, action := range []string{"pause", "detach", "turn"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				f, s, _ := newProposalFixture(t, describeReply)
				started, release := make(chan struct{}), make(chan struct{})
				if kind == "folder" {
					f.handler.workspaceBuild.Folders = &barrierBuildFolders{started: started, release: release}
				} else {
					f.handler.workspaceBuild.Links = &barrierBuildLinks{started: started, release: release}
				}
				requestID := uuid.NewString()
				req := map[string]any{"request_id": requestID, "version": s.Version}
				if kind == "folder" {
					req["mode"] = "picker"
				} else {
					req["url"] = "https://example.com"
				}
				done := make(chan map[string]any, 1)
				go func() {
					_, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/"+kind, req)
					done <- body
				}()
				<-started
				current := readProposalBuild(t, f, s.ID)
				switch action {
				case "pause":
					// The tab has not heard the admission's new version yet.
					f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/pause", map[string]any{"version": s.Version, "attachment_id": requestID})
				case "detach":
					f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+s.ID+"/"+kind+"/detach", map[string]any{"version": current.Version})
				case "turn":
					f.turn(t, current, map[string]any{"text": "Use my current draft instead"})
				}
				close(release)
				if body := <-done; body["code"] != "version" {
					t.Fatalf("late attachment survived %s: %v", action, body)
				}
				current = readProposalBuild(t, f, s.ID)
				if current.LinkRequestID != "" || current.FolderRequestID != "" {
					t.Fatal("pending claim not released")
				}
			})
		}
	}
}

func TestWorkspaceBuildProposalOrdinaryAutoRetryCannotCreate(t *testing.T) {
	malicious := strings.ReplaceAll(describeReply, `"create_now":false`, `"create_now":true`)
	f := newBuildFixture(t, false, malicious, malicious, malicious)
	s := f.start(t)
	_, body := f.turn(t, s, map[string]any{"text": "A newsletter"})
	s = sessionOf(t, body)
	f.provider.errs = []error{nil, errors.New("offline")}
	_, body = f.turn(t, s, map[string]any{"text": "(I changed the blueprint)", "auto": true})
	s = sessionOf(t, body)
	if s.Retry == nil || !s.Retry.Auto || s.TurnCount != 1 {
		t.Fatal("lost auto authority/provenance")
	}
	_, body = f.turn(t, s, map[string]any{"choice_id": "try_again"})
	s = sessionOf(t, body)
	if s.CreateNow || s.TurnCount != 1 || s.FirstRequest != "A newsletter" {
		t.Fatal("auto retry became confirmation")
	}
}
