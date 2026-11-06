package filejanitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFilingReview_SavedOnlyAndIndependentOfCurator(t *testing.T) {
	s, ws, root := curatorWelcomeFixture(t)
	before, err := s.store.LoadScanState(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.store.LoadSettings(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	ws.AgentInstances = nil // review is useful even with no reachable conversation
	for range 2 {
		got, err := s.ReadFilingReview(t.Context(), "local", ws.ID)
		if err != nil || got.State != "ready" || got.Pending != 2 || got.NeedsReview != 1 || got.RootID != settings.RootID || got.BatchID != before.Batches[0].ID || got.BatchCompletedAt == nil || got.ObservedAt.IsZero() {
			t.Fatalf("saved review: %+v %v", got, err)
		}
		data, _ := json.Marshal(got)
		for _, private := range []string{root, "report.pdf", "uncertain.qqq", "fingerprint", "approval", "candidate_ids"} {
			if strings.Contains(string(data), private) {
				t.Fatalf("private data escaped: %s", data)
			}
		}
	}
	after, err := s.store.LoadScanState(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.store.LoadSettings(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(settings, fresh) {
		t.Fatal("projection changed filing state")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("projection touched the managed root")
	}
}

func TestFilingReview_UsesCanonicalLatestPendingSummary(t *testing.T) {
	s, ws, root := curatorWelcomeFixture(t)
	states := []CandidateState{CandidatePending, CandidateApproved, CandidateSkipped, CandidateApplied, CandidateFailed, CandidateStale, CandidateApplying}
	for i := range states {
		agedFile(t, root, fmt.Sprintf("new-%d.pdf", i), 100)
	}
	batch, created, err := s.ScanNow(ws.ID, ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("fixture scan: %v %v", created, err)
	}
	if batch.RootID == "" || batch.CapabilityInstalledAt == nil {
		t.Fatal("scan did not save scope provenance")
	}
	if _, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error {
		n := 0
		for i := range state.Candidates {
			if state.Candidates[i].BatchID != batch.ID {
				continue
			}
			state.Candidates[i].State, state.Candidates[i].NeedsReview = states[n], true
			n++
		}
		last := len(state.Batches) - 1
		state.Batches[last] = SummarizeBatch(state.Batches[last], state.CandidatesFor(batch.ID))
		state.Batches[last].Summary = BatchSummary{Proposed: 999, NeedsReview: 999} // recompute, don't trust cached counts
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	selected, candidates, found, err := s.LatestPendingBatch(ws.ID)
	if err != nil || !found {
		t.Fatal("canonical selection", err)
	}
	canonical := SummarizeBatch(selected, candidates)
	got, err := s.ReadFilingReview(t.Context(), "local", ws.ID)
	if err != nil || got.BatchID != batch.ID || got.Pending != canonical.Summary.Proposed || got.NeedsReview != canonical.Summary.NeedsReview || got.Pending != 2 {
		t.Fatalf("canonical partial result: %+v %v; want %+v", got, err, canonical.Summary)
	}
	welcome, err := s.ReadCuratorWelcome(t.Context(), "local", ws.ID, "curator")
	if err != nil || welcome.Pending != got.Pending || welcome.NeedsReview != got.NeedsReview {
		t.Fatalf("welcome disagrees: %+v %v", welcome, err)
	}
}

func TestFilingReview_DistinguishesZeroUnconfiguredAndAbsent(t *testing.T) {
	s, ws, _ := curatorWelcomeFixture(t)
	if _, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error {
		for i := range state.Candidates {
			state.Candidates[i].State = CandidateSkipped
		}
		for i := range state.Batches {
			state.Batches[i] = SummarizeBatch(state.Batches[i], state.CandidatesFor(state.Batches[i].ID))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadFilingReview(t.Context(), "local", ws.ID)
	if err != nil || got.State != "ready" || got.ReviewCounts == nil || got.Pending != 0 || got.BatchID != "" {
		t.Fatalf("known zero: %+v %v", got, err)
	}
	if _, err := s.store.UpdateSettings(ws.ID, func(settings *JanitorSettings) error { settings.AccessRevokedAt = time.Now(); return nil }); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadFilingReview(t.Context(), "local", ws.ID)
	if err != nil || got.State != "unconfigured" || got.ReviewCounts != nil || got.RootID != "" {
		t.Fatalf("unconfigured: %+v %v", got, err)
	}
	ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
	got, err = s.ReadFilingReview(t.Context(), "local", ws.ID)
	if err != nil || got.State != "not_applicable" || got.ReviewCounts != nil || got.CapabilityID != "" {
		t.Fatalf("removed: %+v %v", got, err)
	}
}

func TestFilingReview_RejectsStaleOrForeignScope(t *testing.T) {
	for _, mode := range []string{"owner", "owner case", "trash", "missing", "root", "reinstall", "grant", "reference", "candidate scope", "private batch id"} {
		t.Run(mode, func(t *testing.T) {
			s, ws, _ := curatorWelcomeFixture(t)
			owner := "local"
			switch mode {
			case "owner":
				owner = "other"
			case "owner case":
				owner = "LOCAL"
			case "trash":
				ws.Status = workspace.StatusTrashed
			case "missing":
				ws.Status = workspace.StatusMissing
			case "root":
				if _, err := s.store.UpdateSettings(ws.ID, func(settings *JanitorSettings) error { settings.RootID = "different-generation"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "reinstall":
				ws.InstalledCapabilities[0].InstalledAt = time.Now().Add(time.Hour)
			case "grant":
				ws.MCPBindings[0].Enabled = false
			case "reference":
				ws.DirectoryReferences[0].Path = "/not-approved"
			case "candidate scope":
				if _, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error { state.Candidates[0].BatchID = "other-batch"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "private batch id":
				if _, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error {
					state.Batches[0].ID = "/private/filename.pdf"
					for i := range state.Candidates {
						state.Candidates[i].BatchID = state.Batches[0].ID
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.ReadFilingReview(t.Context(), owner, ws.ID)
			if !errors.Is(err, ErrReviewUnavailable) || got.ReviewCounts != nil || got.BatchID != "" {
				t.Fatalf("stale scope escaped: %+v %v", got, err)
			}
		})
	}
}

func TestFilingReview_RechecksRootInstallAndOwnerAfterRead(t *testing.T) {
	for _, mode := range []string{"owner", "root", "install", "removal"} {
		t.Run(mode, func(t *testing.T) {
			s, ws, _ := curatorWelcomeFixture(t)
			s.workspaces = &changingCuratorWorkspace{WorkspaceStore: s.workspaces, change: func() {
				switch mode {
				case "owner":
					ws.OwnerUserID = "other"
				case "install":
					ws.InstalledCapabilities[0].InstalledAt = time.Now().Add(time.Hour)
				case "removal":
					ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
				case "root":
					if _, err := s.store.UpdateSettings(ws.ID, func(settings *JanitorSettings) error { settings.RootID = "next-root"; return nil }); err != nil {
						t.Fatal(err)
					}
				}
			}}
			got, err := s.ReadFilingReview(t.Context(), "local", ws.ID)
			if err == nil || got.ReviewCounts != nil {
				t.Fatalf("late scope escaped: %+v %v", got, err)
			}
		})
	}
}

type missingReviewFolder struct{ WorkspaceStore }

func (missingReviewFolder) GetFolderWorkspace(string) (*workspace.Workspace, error) {
	return nil, errors.New("synthetic unavailable folder")
}

func TestFilingReview_CannotFallBackFromMissingCanonicalFolder(t *testing.T) {
	s, ws, _ := curatorWelcomeFixture(t)
	s.workspaces = missingReviewFolder{s.workspaces}
	got, err := s.ReadFilingReview(t.Context(), "local", ws.ID)
	if err == nil || got.ReviewCounts != nil {
		t.Fatalf("fallback grant: %+v %v", got, err)
	}
}

func TestFilingReview_LegacyBatchRemainsDiscoverableButCannotBeReinstalled(t *testing.T) {
	s, ws, _ := curatorWelcomeFixture(t)
	if _, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error {
		state.Batches[0].RootID, state.Batches[0].CapabilityInstalledAt = "", nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadFilingReview(t.Context(), "local", ws.ID)
	if err != nil || got.Pending != 2 {
		t.Fatalf("legacy batch hidden: %+v %v", got, err)
	}
	ws.InstalledCapabilities[0].InstalledAt = time.Now().Add(time.Hour)
	if _, err := s.ReadFilingReview(t.Context(), "local", ws.ID); err == nil {
		t.Fatal("explicit reinstall adopted an older batch")
	}
	ws.InstalledCapabilities[0].Source = workspace.InstallSourceLegacyMigration
	got, err = s.ReadFilingReview(t.Context(), "local", ws.ID)
	if err != nil || got.Pending != 2 {
		t.Fatalf("additive migration hid legacy work: %+v %v", got, err)
	}
}
