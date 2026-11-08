package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderpath"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

/*
The notes half of a confirmed Personal HQ setup (assistant-hq-onboarding 3.1–3.4).

The HQ request journal owns the confirmed notes choice until HQ activation; from
then on this file's operation record owns it. The operation is recorded before
anything is written, names its reviewed folder exactly, and finishes
independently of HQ: HQ stays active whatever happens here, and a retry makes the
same folder or stops with guidance. It never replaces a connection, adopts a
folder it did not make, or removes anything the user may have touched.
*/

const secondBrainActionSetup = "setup"

var (
	// ErrSecondBrainDestinationChanged refuses a confirmation whose reviewed
	// notes location is no longer where the default would put them: the
	// Workspace Directory changed after the review. The client shows the new
	// location and asks again rather than silently using it.
	ErrSecondBrainDestinationChanged = errors.New("personal assistant: the notes location changed since you reviewed it")
	// ErrSecondBrainNoSetup says there is no notes setup to retry.
	ErrSecondBrainNoSetup = errors.New("personal assistant: there is no notes setup to retry")
)

// Notes setup outcomes a client switches on.
const (
	SecondBrainSetupReady   = "ready"
	SecondBrainSetupPending = secondbrain.SetupPending
	SecondBrainSetupFailed  = secondbrain.SetupFailed
	SecondBrainSetupStopped = secondbrain.SetupStopped
	// SecondBrainSetupNone means no notes setup was confirmed (deferred, or a
	// legacy request).
	SecondBrainSetupNone = "none"
)

// SecondBrainSetupDestination is a reviewed notes folder, resolved once.
type SecondBrainSetupDestination struct {
	Parent string
	Name   string
}

// SecondBrainSetupResult is the notes half of an HQ setup response, and of the
// notes status read. Path is shown to the user; Message is safe to show.
type SecondBrainSetupResult struct {
	Status      string `json:"status"`
	OperationID string `json:"operation_id,omitempty"`
	Path        string `json:"path,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Message     string `json:"message,omitempty"`
	// Writable is true when the connected folder is one Ori set up, so a quick
	// note can be saved to it; false for a folder the user connected.
	Writable bool `json:"writable,omitempty"`
}

// SecondBrainSetupOperationID derives the notes operation from the HQ request,
// stably and within the notes request-id shape.
func SecondBrainSetupOperationID(hqRequestID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(hqRequestID)))
	return "hq-notes-" + hex.EncodeToString(sum[:])[:24]
}

// ResolveSetupDestination fixes where a confirmed setup will put the notes.
//
// With a selection token it is that chosen parent, checked by the folder rules,
// under name. Without one it is the default place under the default name, and
// witness — the folder the user reviewed — must still be exactly that place. It
// writes nothing.
func (s *SecondBrainService) ResolveSetupDestination(witness, selectionToken, name string) (SecondBrainSetupDestination, error) {
	if s == nil {
		return SecondBrainSetupDestination{}, ErrSecondBrainUnavailable
	}
	if strings.TrimSpace(selectionToken) != "" {
		name = strings.TrimSpace(name)
		if !secondbrain.ValidFolderName(name) {
			return SecondBrainSetupDestination{}, &SecondBrainFolderError{Message: "Choose a simple folder name, without slashes."}
		}
		checked, err := s.checkedFolder(selectionToken)
		if err != nil {
			return SecondBrainSetupDestination{}, err
		}
		return SecondBrainSetupDestination{Parent: checked.Path, Name: name}, nil
	}
	parent := s.defaultParent()
	if parent == "" {
		return SecondBrainSetupDestination{}, ErrSecondBrainSelection
	}
	parent = filepath.Clean(parent)
	target := filepath.Join(parent, secondbrain.DefaultFolderName)
	if strings.TrimSpace(witness) == "" || filepath.Clean(strings.TrimSpace(witness)) != target {
		return SecondBrainSetupDestination{}, ErrSecondBrainDestinationChanged
	}
	return SecondBrainSetupDestination{Parent: parent, Name: secondbrain.DefaultFolderName}, nil
}

// PrepareSetup records a confirmed notes setup before anything is written. It
// is idempotent by operation id: a replay changes nothing, and an operation
// that already finished (its receipt is remembered) is not reopened.
func (s *SecondBrainService) PrepareSetup(ctx context.Context, userID, operationID, hqRequestID string, dest SecondBrainSetupDestination) error {
	if s == nil {
		return ErrSecondBrainUnavailable
	}
	if !secondbrain.ValidRequestID(operationID) || !filepath.IsAbs(dest.Parent) || !secondbrain.ValidFolderName(dest.Name) {
		return ErrValidation
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()
	_, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, done := doc.Receipt(operationID); done {
			return nil
		}
		if doc.Setup != nil && doc.Setup.OperationID == operationID {
			return nil
		}
		now := s.deps.Now().UTC()
		doc.Setup = &secondbrain.SetupOperation{
			OperationID: operationID, HQRequestID: strings.TrimSpace(hqRequestID),
			Parent: filepath.Clean(dest.Parent), Name: dest.Name,
			Status: secondbrain.SetupPending, CreatedAt: now, UpdatedAt: now,
		}
		return nil
	})
	return err
}

// RunSetup carries out the recorded operation: it makes and connects the
// reviewed folder, or records why it stopped. It never touches HQ.
func (s *SecondBrainService) RunSetup(ctx context.Context, userID, operationID string) (SecondBrainSetupResult, error) {
	if s == nil {
		return SecondBrainSetupResult{}, ErrSecondBrainUnavailable
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()

	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainSetupResult{}, err
	}
	if _, done := doc.Receipt(operationID); done {
		return s.setupResult(doc), nil
	}
	op := doc.Setup
	if op == nil || op.OperationID != operationID {
		return SecondBrainSetupResult{}, ErrSecondBrainNoSetup
	}
	// One notes source at a time: whatever got connected meanwhile stays, and
	// this setup stops rather than replacing or competing with it.
	if connected := doc.Connected(); connected != nil {
		if connected.RequestID == operationID {
			return s.finishSetup(ctx, userID, operationID)
		}
		return s.stopSetup(ctx, userID, operationID, secondbrain.SetupStopped, secondbrain.SetupReasonCurrentSource,
			"Another notes folder is already connected, so a new one was not made. Disconnect it from Notes first if you want Ori to set one up.")
	}

	// The reviewed place is never swapped for another. The parent is pinned by
	// the creator (a real folder, no link swapped in); the folder rules apply to
	// the folder made in it, as they do for an ordinary setup — the Workspace
	// Directory itself is a valid parent but never a notes folder.
	if s.deps.CheckFolder == nil {
		return s.stopSetup(ctx, userID, operationID, secondbrain.SetupFailed, secondbrain.SetupReasonUnavailable,
			"Notes couldn't be set up right now. Try again.")
	}
	if info, statErr := os.Stat(op.Parent); statErr != nil || !info.IsDir() {
		return s.stopSetup(ctx, userID, operationID, secondbrain.SetupFailed, secondbrain.SetupReasonMissingParent,
			"The folder your notes were going into isn't there anymore. Put it back, or choose another location.")
	}

	creation, err := secondbrain.Creator{Provenance: operationID}.Create(op.Parent, op.Name, s.readSeeds(ctx, userID))
	fresh := err == nil
	if errors.Is(err, secondbrain.ErrTargetNotEmpty) {
		// Occupied: only this operation's own half-made folder is finished.
		creation, err = secondbrain.Creator{}.Recover(op.Parent, op.Name, operationID)
		if errors.Is(err, secondbrain.ErrNotOwnSetup) {
			return s.stopSetup(ctx, userID, operationID, secondbrain.SetupFailed, secondbrain.SetupReasonOccupied,
				"A folder named \""+op.Name+"\" is already there, so Ori left it alone. Move or rename it, then retry. To use another place, set up notes from Notes instead.")
		}
	}
	if err != nil {
		logger.Warn("A notes setup could not make its folder", logger.Fields{"operation_id": operationID, "error": err.Error()})
		reason := secondbrain.SetupReasonUnwritable
		if errors.Is(err, fs.ErrNotExist) {
			reason = secondbrain.SetupReasonMissingParent
		}
		return s.stopSetup(ctx, userID, operationID, secondbrain.SetupFailed, reason,
			"Ori couldn't make the notes folder there. Check that you can write to that location, then try again.")
	}
	target, err := s.deps.CheckFolder(creation.Path)
	if err != nil {
		if fresh {
			creation.Undo()
		}
		return s.stopSetup(ctx, userID, operationID, secondbrain.SetupFailed, secondbrain.SetupReasonUnwritable,
			folderpath.Message(err))
	}
	kind := secondbrain.KindMarkdown
	if secondbrain.IsObsidianVault(target.Path) {
		kind = secondbrain.KindObsidian
	}
	source := secondbrain.Source{
		ID: secondbrain.SourceID(target.Path), Kind: kind, Name: target.Name, Path: target.Path,
		ConnectedAt: s.deps.Now().UTC(), RequestID: operationID, Origin: secondbrain.OriginCreatedByOri,
	}
	var lookID string
	updated, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		lookID = ""
		if _, done := doc.Receipt(operationID); done {
			return nil
		}
		if doc.Setup == nil || doc.Setup.OperationID != operationID {
			return ErrSecondBrainNoSetup
		}
		if doc.Connected() != nil {
			return ErrSecondBrainAlreadyConnected
		}
		connected, err := doc.Connect(source, false)
		if err != nil {
			return err
		}
		look := s.runningLook(operationID)
		connected.Current, lookID = &look, look.ID
		at := s.deps.Now().UTC()
		// The review said what Ori keeps up to date before anything was written.
		connected.FillIn = &secondbrain.FillIn{Consent: secondbrain.ConsentAllowed, ConsentAt: &at, ConsentRequestID: operationID}
		// Not asked, not skipped: the project question stays a later, explicit
		// action, and nothing opens it on its own after setup.
		connected.FillIn.ProjectAsk, connected.FillIn.ProjectAskAt = secondbrain.ProjectDeferred, &at
		doc.Remember(secondbrain.Receipt{
			RequestID: operationID, Action: secondBrainActionSetup, At: s.deps.Now(), LookID: lookID,
			Created: creation.FollowUps, Skipped: creation.People,
		})
		doc.Setup = nil
		return nil
	})
	if err != nil {
		if fresh {
			creation.Undo()
		}
		if errors.Is(err, ErrSecondBrainAlreadyConnected) {
			return s.stopSetup(ctx, userID, operationID, secondbrain.SetupStopped, secondbrain.SetupReasonCurrentSource,
				"Another notes folder was connected while this one was being set up, so it was not connected. Disconnect it from Notes first if you want this one.")
		}
		return SecondBrainSetupResult{}, err
	}
	if lookID != "" {
		// The setup receipt: rules only, over the folder Ori just made. No model
		// and no read of any folder the user already had.
		setup := secondbrain.SetupCounts{FollowUps: creation.FollowUps, People: creation.People}
		s.startLook(userID, *updated.Connected(), nil, lookID, operationID, &setup)
		for _, fn := range s.onConnected {
			fn(ctx, userID, *updated.Connected())
		}
	}
	return s.setupResult(updated), nil
}

// RetrySetup re-runs the recorded notes setup, exactly as it was reviewed.
func (s *SecondBrainService) RetrySetup(ctx context.Context, userID string) (SecondBrainSetupResult, error) {
	if s == nil {
		return SecondBrainSetupResult{}, ErrSecondBrainUnavailable
	}
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainSetupResult{}, err
	}
	if doc.Setup == nil {
		return SecondBrainSetupResult{}, ErrSecondBrainNoSetup
	}
	return s.RunSetup(ctx, userID, doc.Setup.OperationID)
}

// SetupStatus projects the notes setup for the HQ card and Today.
func (s *SecondBrainService) SetupStatus(ctx context.Context, userID string) (SecondBrainSetupResult, error) {
	if s == nil {
		return SecondBrainSetupResult{}, ErrSecondBrainUnavailable
	}
	doc, err := s.read(ctx, userID)
	if errors.Is(err, ErrNeedsHQ) {
		return SecondBrainSetupResult{Status: SecondBrainSetupNone}, nil
	}
	if err != nil {
		return SecondBrainSetupResult{}, err
	}
	return s.setupResult(doc), nil
}

func (s *SecondBrainService) finishSetup(ctx context.Context, userID, operationID string) (SecondBrainSetupResult, error) {
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if doc.Setup != nil && doc.Setup.OperationID == operationID {
			doc.Setup = nil
		}
		return nil
	})
	if err != nil {
		return SecondBrainSetupResult{}, err
	}
	return s.setupResult(doc), nil
}

func (s *SecondBrainService) stopSetup(ctx context.Context, userID, operationID, status, reason, message string) (SecondBrainSetupResult, error) {
	if len(message) > 300 {
		message = message[:300]
	}
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if doc.Setup == nil || doc.Setup.OperationID != operationID {
			return ErrSecondBrainNoSetup
		}
		next := *doc.Setup
		next.Status, next.Reason, next.Message, next.UpdatedAt = status, reason, message, s.deps.Now().UTC()
		doc.Setup = &next
		return nil
	})
	if err != nil {
		return SecondBrainSetupResult{}, err
	}
	return s.setupResult(doc), nil
}

// setupResult reads the outcome from the record: a pending or stopped
// operation, else the connection, else none.
func (s *SecondBrainService) setupResult(doc SecondBrainDocument) SecondBrainSetupResult {
	if op := doc.Setup; op != nil {
		return SecondBrainSetupResult{
			Status: op.Status, OperationID: op.OperationID, Path: op.Target(),
			Reason: op.Reason, Message: op.Message,
		}
	}
	if connected := doc.Connected(); connected != nil {
		return SecondBrainSetupResult{
			Status: SecondBrainSetupReady, OperationID: connected.RequestID, Path: connected.Path,
			Writable: connected.CreatedByOri(),
		}
	}
	return SecondBrainSetupResult{Status: SecondBrainSetupNone}
}
