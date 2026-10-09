package personalassistant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func TestFolderReviewOptions_DescribingChoicesDoesNotRescanOrChooseScope(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	creator := &fakeFolderCreator{}
	f.service.deps.Creator = creator
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	noScan := func(string) (folderdigest.Result, error) {
		t.Fatal("describing options rescanned the source")
		return folderdigest.Result{}, nil
	}
	f.service.deps.Scan, f.service.deps.ScanObservation = noScan, noScan
	// Missing and empty specialized plans both withdraw that option; plain
	// folders remain independently reviewable. No preferred or selected scope
	// is encoded merely because only some observed folders can be set up.
	baseline := service.ReviewOptions(ctx, target, observation.ID)
	f.service.deps.Setup = &fakeFolderSetup{}
	withEmptyPlan := service.ReviewOptions(ctx, target, observation.ID)
	if len(baseline) == 0 || len(withEmptyPlan) != len(baseline) {
		t.Fatalf("empty plan changed compatible choices: %+v / %+v", baseline, withEmptyPlan)
	}
	for _, option := range withEmptyPlan {
		data, err := json.Marshal(option)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{f.home, "offer_id", "destination_id", "recommended", "selected", "plan_digest"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("description invented private/executable/default scope: %s", data)
			}
		}
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || after.Version != before.Version || f.ids != ids || len(after.Offers) != len(before.Offers) || len(creator.requests) != 0 {
		t.Fatal("read-only choices allocated or executed setup")
	}
}

func TestFolderReviewOptions_ReadFailureWithdrawsDescriptions(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Mutate(ctx, target.UserID, func(*FolderDigestDocument) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.folder.path, ".ori", folderDigestFileName)
	data, err := os.ReadFile(path) // #nosec G304 -- fixture-owned temporary sidecar, not a source file
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("read failure invented options", got)
	}
	if f.ids != ids {
		t.Fatal("description allocated an offer")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || after.Version != before.Version {
		t.Fatal("failed description mutated the sidecar", err)
	}
}

func TestFolderReviewOptions_ReadOnlyAvailabilityAndScope(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	creator := &fakeFolderCreator{}
	f.service.deps.Creator = creator
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	options := service.ReviewOptions(ctx, target, observation.ID)
	if len(options) < 2 {
		t.Fatalf("missing generic choices: %+v", options)
	}
	for _, option := range options {
		candidate, ok := observedReviewCandidate(service.selections[observation.ID], option.CandidateID)
		if !ok || option.WorkspaceType == "" || option.Presentation == nil || option.Presentation.Version != 1 {
			t.Fatal("invented option", option)
		}
		if candidate.Name == "Album" {
			t.Fatal("specialized setup offered without a plan")
		}
	}
	f.service.deps.Setup = &fakeFolderSetup{lines: oneCardLines()}
	withSetup := service.ReviewOptions(ctx, target, observation.ID)
	if len(withSetup) <= len(options) {
		t.Fatal("available specialized setup not offered", withSetup)
	}
	f.service.deps.Setup = &fakeFolderSetup{planErr: ErrFolderOutcomeUnavailable}
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != len(options) {
		t.Fatal("unavailable plan offered", got)
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || before.Version != after.Version || f.ids != ids || len(creator.requests) != 0 {
		t.Fatal("suggestions wrote offers, allocated IDs, or executed setup")
	}
	f.service.deps.Creator = nil
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("unavailable creator offered", got)
	}
	f.service.deps.Creator = creator
	foreign := target
	foreign.DraftID = "other"
	if got := service.ReviewOptions(ctx, foreign, observation.ID); len(got) != 0 {
		t.Fatal("foreign selection offered")
	}
	pending, err := f.service.ScanChip(ctx, target.UserID, "documents")
	if err != nil {
		t.Fatal(err)
	}
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("pending review ignored")
	}
	doc, err := f.store.Read(ctx, target.UserID)
	if err != nil || doc.Pending().ID != pending.ID {
		t.Fatal("pending review displaced")
	}
	f.now = f.now.Add(foldercontext.SelectionTTL + time.Second)
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("expired selection offered")
	}
}
