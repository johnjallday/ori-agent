package projectlibrary

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
)

// Characterize the existing later-Open boundary, independently of Map: a real
// child can outlive a conflicting catalog association. A fresh create review
// must not adopt it; the Home's exact-link review is the separate recovery owner.
func TestActivation_SetupOpenConcurrentCatalogChangeRequiresExplicitLinkRecovery(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	factory := realActivationCreator(t, scope, file, installed)
	created, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		return &pauseAfterActivationCreator{ActivationCreator: factory(resolver), created: created, release: release}
	})
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := service.CommitSetupOpen(t.Context(), scope, "single", review.Token, "setup-open-race", "source-offer")
		result <- err
	}()
	select {
	case <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("canonical child was not created")
	}
	// An independent, genuine Home edit between child creation and association.
	note := "Review the alternate melody"
	patch := FieldsPatch{NextAction: &note}
	edit, err := a.library.ReviewFields(scope, "alternates", 0, patch, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.library.CommitFields(scope, "alternates", edit.Token, "concurrent-home-edit", 0, patch, scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("expected catalog conflict after the real child: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open did not return after catalog conflict")
	}
	doc, err = a.library.Read(scope)
	if err != nil || sessionEntry(doc, "single").Link != nil {
		t.Fatalf("failed association became catalog success: %+v %v", doc, err)
	}
	eligibility, err := a.Eligibility(t.Context(), scope, "single")
	if err != nil || eligibility.State != "folder_owned" {
		t.Fatalf("fresh setup must expose the already-owned source: %+v %v", eligibility, err)
	}
	if _, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("fresh create review should refuse the already-owned source: %v", err)
	}
	pending, err := a.library.PendingLinkedProjects(scope)
	if err != nil || pending.Total != 1 || len(pending.Rows) != 1 {
		t.Fatalf("the one durable child was not offered for exact-link review: %+v %v", pending, err)
	}
	link, err := a.library.ReviewLinkedProject(scope, pending.Rows[0].WorkspaceID, pending.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.library.CommitLinkedProject(scope, pending.Rows[0].WorkspaceID, link.Token, "explicit-link-recovery"); err != nil {
		t.Fatal(err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("recovery duplicated a child or changed source files: %v %v", ids, err)
	}
	doc, err = a.library.Read(scope)
	if err != nil || sessionEntry(doc, "alternates").Fields.NextAction != note {
		t.Fatal("recovery overwrote the competing Home edit", err)
	}
}
