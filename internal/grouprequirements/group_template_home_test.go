package grouprequirements

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	testGroupTemplateID       = "group-template:0123456789abcdef0123456789abcdef"
	testGroupTemplateRevision = "1111111111111111111111111111111111111111111111111111111111111111"
)

func groupTemplateHomeInput(t *testing.T, template projecttemplates.Template, name string) Input {
	t.Helper()
	digest, err := DigestInput(map[string]any{
		"domain": "group_template_home:v1", "group_template_id": testGroupTemplateID,
		"revision": testGroupTemplateRevision, "name": name,
	})
	if err != nil {
		t.Fatal(err)
	}
	return Input{
		OwnerUserID: "owner-1", OperationKind: OperationPrepareHome, Template: template,
		Composition: CompositionGrouped, CreateHome: true, InputDigest: digest, HomeName: name,
		GroupTemplateID: testGroupTemplateID, GroupTemplateRevision: testGroupTemplateRevision,
	}
}

type failFirstOperationSave struct {
	*MemoryStore
	failed bool
}

func (store *failFirstOperationSave) SaveOperation(ctx context.Context, operation Operation) error {
	if !store.failed {
		store.failed = true
		return errors.New("receipt database unavailable")
	}
	return store.MemoryStore.SaveOperation(ctx, operation)
}

func TestGroupTemplateHomeCreatesReviewedNameWithProvenanceOnly(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	input := groupTemplateHomeInput(t, template, "Lab Portfolio")

	review, err := service.Review(context.Background(), input)
	if err != nil || review.Token == "" || review.HomeName != "Lab Portfolio" || !review.HomeWillBeCreated {
		t.Fatalf("review = (%#v, %v)", review, err)
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Fatalf("review created workspaces %v", ids)
	}
	claim, err := service.Claim(context.Background(), input, review.Token, "create-lab")
	if err != nil || !claim.HomeCreated || claim.Operation.ChildWorkspaceID != "" || claim.Operation.ProjectLinkID != "" || claim.Snapshot != nil {
		t.Fatalf("claim = (%#v, %v)", claim, err)
	}
	home, err := store.Get(claim.Operation.HomeWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := home.GetAssistantProgramState().GroupTemplate
	if home.Name != "Lab Portfolio" || provenance == nil || provenance.ReviewDigest != claim.Operation.ReviewDigest ||
		provenance.GroupTemplateID != testGroupTemplateID || provenance.SourceKind != "plugin" || provenance.PluginOwner == nil ||
		provenance.PluginOwner.PluginID != "neutral" || provenance.HomeDigest != projecttemplates.GroupTemplateHomeDigest(template.AssistantProgram) {
		t.Fatalf("created Home = name %q provenance %#v", home.Name, provenance)
	}
	if home.GetTemplateProvenance() != nil || len(home.GetAgentInstances()) != 0 {
		t.Fatal("group template Home acquired project provenance or agents")
	}
}

func TestGroupTemplateHomeReviewBindsNameSelectionAndOperation(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	input := groupTemplateHomeInput(t, template, "Lab Portfolio")
	review, err := service.Review(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	renamed := groupTemplateHomeInput(t, template, "Different Name")
	if _, err := service.Claim(context.Background(), renamed, review.Token, "renamed"); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("changed name claim error = %v, want stale", err)
	}
	moved := input
	moved.GroupTemplateRevision = strings.Repeat("2", 64)
	if _, err := service.Claim(context.Background(), moved, review.Token, "moved"); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("changed selection revision claim error = %v, want stale", err)
	}
	direct := input
	direct.GroupTemplateID, direct.GroupTemplateRevision, direct.HomeName = "", "", ""
	if _, err := service.Claim(context.Background(), direct, review.Token, "direct"); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("receipt satisfied an unbound Home-only claim: %v", err)
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Fatalf("stale claims created workspaces %v", ids)
	}

	invalid := groupTemplateHomeInput(t, template, "bad/name")
	if got := service.Evaluate(invalid); got.State != StateContractInvalid {
		t.Fatalf("invalid name evaluation = %#v", got)
	}
	project := testInput(t, template, CompositionGrouped, false)
	project.GroupTemplateID, project.GroupTemplateRevision = testGroupTemplateID, testGroupTemplateRevision
	project.OperationKind = OperationPrepareHome
	project.CreateHome = true
	project.GroupTemplateRevision = ""
	if _, err := service.Review(context.Background(), project); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("incomplete selection review error = %v", err)
	}
}

func TestGroupTemplateHomeReuseNeverRenamesOrBackfills(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	key, err := programKey("owner-1", template)
	if err != nil {
		t.Fatal(err)
	}

	input := groupTemplateHomeInput(t, template, "Typed Name")
	review, err := service.Review(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Another confirmed path creates the exact Home between review and commit.
	guided, _, err := workspace.NewAssistantProgramStore(store).EnsureNamedStation(key, template.AssistantProgram, "Guided Home")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(context.Background(), input, review.Token, "concurrent")
	if err != nil || claim.HomeCreated || claim.Operation.HomeWorkspaceID != guided.ID {
		t.Fatalf("concurrent claim = (%#v, %v)", claim, err)
	}
	home, _ := store.Get(guided.ID)
	if home.Name != "Guided Home" || home.GetAssistantProgramState().GroupTemplate != nil {
		t.Fatalf("reuse renamed or backfilled the Home: %q %#v", home.Name, home.GetAssistantProgramState().GroupTemplate)
	}

	again := groupTemplateHomeInput(t, template, "Another Typed Name")
	reuse, err := service.Review(context.Background(), again)
	if err != nil || reuse.HomeWorkspaceID != guided.ID || reuse.HomeName != "Guided Home" || reuse.HomeWillBeCreated {
		t.Fatalf("existing Home review = (%#v, %v)", reuse, err)
	}
	if ids, _ := store.List(); len(ids) != 1 {
		t.Fatalf("reuse created additional workspaces %v", ids)
	}
}

func TestGroupTemplateHomeRetryAfterPartialFailureReportsItsOwnHome(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	receipts := &failFirstOperationSave{MemoryStore: NewMemoryStore()}
	service := NewService(store, receipts)
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	input := groupTemplateHomeInput(t, template, "Lab Portfolio")
	review, err := service.Review(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(context.Background(), input, review.Token, "retry-me"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first claim error = %v, want unavailable after Home write", err)
	}
	ids, _ := store.List()
	if len(ids) != 1 {
		t.Fatalf("partial failure left %v Homes, want exactly one", ids)
	}

	retry, err := service.Claim(context.Background(), input, review.Token, "retry-me")
	if err != nil || retry.HomeCreated || retry.Operation.HomeWorkspaceID != ids[0] {
		t.Fatalf("retry = (%#v, %v)", retry, err)
	}
	home, _ := store.Get(ids[0])
	if provenance := home.GetAssistantProgramState().GroupTemplate; provenance == nil || provenance.ReviewDigest != retry.Operation.ReviewDigest {
		t.Fatalf("retry cannot prove the Home was created by this operation: %#v", provenance)
	}
	if after, _ := store.List(); len(after) != 1 {
		t.Fatalf("retry duplicated the Home: %v", after)
	}
}

func TestHomeOnlyOperationReplaysAfterReviewExpiry(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	input := groupTemplateHomeInput(t, template, "Lab Portfolio")
	review, err := service.Review(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Claim(context.Background(), input, review.Token, "lost-response")
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(ReviewTTL + time.Minute)
	replay, err := service.Claim(context.Background(), input, review.Token, "lost-response")
	if err != nil || !replay.Replayed || replay.Operation.HomeWorkspaceID != first.Operation.HomeWorkspaceID {
		t.Fatalf("expired Home-only replay = (%#v, %v)", replay, err)
	}
	if _, err := service.Claim(context.Background(), input, review.Token, "new-key"); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("expired consumed review accepted a new operation: %v", err)
	}

	unused := groupTemplateHomeInput(t, template, "Lab Portfolio")
	unusedReview, err := service.Review(context.Background(), unused)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(ReviewTTL + time.Minute)
	if _, err := service.Claim(context.Background(), unused, unusedReview.Token, "never-claimed"); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("expired unclaimed review error = %v, want stale", err)
	}
	if ids, _ := store.List(); len(ids) != 1 {
		t.Fatalf("expiry handling created workspaces %v", ids)
	}
}
