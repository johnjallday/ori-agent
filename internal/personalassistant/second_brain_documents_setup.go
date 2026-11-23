package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// DocumentsNotesState reads the Notes ledger only. In particular it does not
// run the Notes panel's connector discovery, look or content previews.
func (s *SecondBrainService) DocumentsNotesState(ctx context.Context, owner string) (secondbrain.Source, bool, bool, error) {
	doc, err := s.read(ctx, owner)
	if err != nil {
		return secondbrain.Source{}, false, false, err
	}
	if c := doc.Connected(); c != nil {
		return *c, true, false, nil
	}
	allowed := documentsSetupAllowed(doc) && s.defaultParent() != ""
	return secondbrain.Source{}, false, allowed, nil
}
func documentsSetupAllowed(doc SecondBrainDocument) bool {
	return (doc.Setup == nil || strings.HasPrefix(doc.Setup.OperationID, "documents-setup-")) && doc.Ask.Decision == "" && doc.Ask.TombstonedAt == nil
}

type DocumentsSetupView struct {
	Path       string
	Ref        string
	Generation string
}

// DocumentsSetup reviews or confirms only the ordinary default notebook.
// Manual Notes setup still owns picking a different parent or repairing one.
func (s *SecondBrainService) DocumentsSetup(ctx context.Context, owner, hq, documents, proposal, expected string, confirm bool) (DocumentsSetupView, error) {
	var out DocumentsSetupView
	s.createMu.Lock()
	defer s.createMu.Unlock()
	doc, err := s.read(ctx, owner)
	if err != nil {
		return out, err
	}
	if doc.Owner.HQWorkspaceID != hq || doc.Connected() != nil || !documentsSetupAllowed(doc) {
		return out, ErrSecondBrainDestinationChanged
	}
	parent, err := filepath.EvalSymlinks(s.defaultParent())
	if err != nil || !filepath.IsAbs(parent) {
		return out, ErrSecondBrainDestinationChanged
	}
	target := filepath.Join(parent, secondbrain.DefaultFolderName)
	if filejanitor.RootsOverlap(target, documents) {
		return out, ErrSecondBrainDestinationChanged
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() {
		return out, ErrSecondBrainDestinationChanged
	}
	witness := directoryid.Witness(info)
	if witness == "" {
		return out, ErrSecondBrainDestinationChanged
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{owner, hq, proposal, target, witness}, "\x00")))
	ref := hex.EncodeToString(sum[:])
	op := "documents-setup-" + ref[:32]
	out = DocumentsSetupView{Path: target, Ref: ref}
	if doc.Setup != nil && doc.Setup.OperationID != op {
		return DocumentsSetupView{}, ErrSecondBrainDestinationChanged
	}
	if !confirm {
		return out, nil
	}
	if expected != ref {
		return DocumentsSetupView{}, ErrSecondBrainDestinationChanged
	}
	if _, err := s.mutate(ctx, owner, func(d *SecondBrainDocument) error {
		if d.Owner.HQWorkspaceID != hq || d.Connected() != nil || !documentsSetupAllowed(*d) {
			return ErrSecondBrainDestinationChanged
		}
		if d.Setup != nil {
			if d.Setup.OperationID != op {
				return ErrSecondBrainDestinationChanged
			}
			return nil
		}
		now := s.deps.Now().UTC()
		d.Setup = &secondbrain.SetupOperation{OperationID: op, Parent: parent, Name: secondbrain.DefaultFolderName, Status: secondbrain.SetupPending, CreatedAt: now, UpdatedAt: now}
		return nil
	}); err != nil {
		return DocumentsSetupView{}, err
	}
	res, err := s.runReviewedSetup(ctx, owner, op, witness)
	if err != nil {
		return out, err
	}
	if res.Status != SecondBrainSetupReady {
		return out, &SecondBrainFolderError{Message: res.Message}
	}
	doc, err = s.read(ctx, owner)
	if err != nil {
		return out, err
	}
	out.Generation = browseReference(doc)
	return out, nil
}
