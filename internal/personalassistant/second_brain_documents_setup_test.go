//go:build unix

package personalassistant

import (
	"errors"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"os"
	"testing"
)

func TestDocumentsSetupCannotRetargetOrReplaceAnHQSetup(t *testing.T) {
	h := newSetupHarness(t)
	ctx := t.Context()
	hq := h.document(t).Owner.HQWorkspaceID
	setup := func(ref string, confirm bool) (DocumentsSetupView, error) {
		return h.service.DocumentsSetup(ctx, "local", hq, h.documents(), "proposal", ref, confirm)
	}
	v, err := setup("", false)
	if err != nil || v.Ref == "" {
		t.Fatal(v, err)
	}
	if err := os.Rename(h.root, h.root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(h.root, 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := setup(v.Ref, true); !errors.Is(err, ErrSecondBrainDestinationChanged) {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.reviewed()); !os.IsNotExist(err) {
		t.Fatal("unreviewed target exists")
	}
	h.prepare(t, "other-hq-notes")
	if _, err := setup("", false); err == nil {
		t.Fatal("hijacked pending HQ setup")
	}
	d := h.document(t)
	if d.Setup.OperationID != "other-hq-notes" || d.Connected() != nil {
		t.Fatal("changed existing operation")
	}
	for _, decision := range []string{secondbrain.DecisionLater, secondbrain.DecisionNone} {
		d.Setup = nil
		d.Ask.Decision = decision
		if documentsSetupAllowed(d) {
			t.Fatal("offered despite decision", decision)
		}
	}
}
