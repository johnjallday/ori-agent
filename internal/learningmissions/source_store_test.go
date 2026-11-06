package learningmissions

import (
	"errors"
	"testing"
)

func TestSourceDeletionRevokesIncarnationEvenWhenIDsAreReimported(t *testing.T) {
	s, db := testService(t)
	offer := propose(t, s)
	incarnation, err := s.store.SourceIncarnation(t.Context(), "local", "workspace")
	if err != nil || incarnation != offer.Instance.Definition.Incarnation {
		t.Fatalf("local source not pinned: %q %v", incarnation, err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id='workspace'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SourceIncarnation(t.Context(), "local", "workspace"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("deleted source remains authorized: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('workspace','Practice',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SourceIncarnation(t.Context(), "local", "workspace"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("reimported ID revived the old source incarnation")
	}
	if _, err := s.store.Instance(t.Context(), "local", offer.Instance.ID); err != nil {
		t.Fatal("source removal erased learning history rather than revoking evidence access")
	}
}
