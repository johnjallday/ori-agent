package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

var (
	ErrFolderSelection      = errors.New("folder selection is no longer available; pick again")
	ErrFolderSelectionLimit = errors.New("too many folder previews; try again after older previews expire")
)

const (
	folderSelectionsPerOwner = 16
	folderSelectionsMax      = 128
)

type heldFolderObservation struct {
	target      foldercontext.Target
	binding     KnowledgeBinding
	root        string
	identity    string
	result      folderdigest.Result
	observation foldercontext.Observation
	saved       bool // once consumed, this ID can never masquerade as fresh staging
}

// FolderObservationService borrows the trusted picker/scan dependencies, never
// the digest's mutating ScanChip/ScanPicked APIs. Paths remain process-local.
type FolderObservationService struct {
	digest      *FolderDigestService
	mu          sync.Mutex
	selections  map[string]heldFolderObservation
	busy        map[string]bool
	reviewGuard func(context.Context, FolderOffer) error
	reviewLease func(foldercontext.Target) (func(), bool)
}

func NewFolderObservationService(digest *FolderDigestService) *FolderObservationService {
	return &FolderObservationService{digest: digest, selections: make(map[string]heldFolderObservation), busy: make(map[string]bool)}
}

// Choices reads only availability, never an offer or source directory listing.
func (s *FolderObservationService) Choices(ctx context.Context, userID string) (FolderDigestView, error) {
	if s == nil || s.digest == nil || s.digest.store == nil {
		return FolderDigestView{}, ErrRepairNeeded
	}
	if _, err := s.digest.store.Binding(ctx, userID); err != nil {
		return FolderDigestView{}, err
	}
	view := FolderDigestView{Chips: s.digest.availableChips()}
	if picker := s.digest.deps.Picker; picker != nil {
		view.PickerAvailable = picker.Available()
		if !view.PickerAvailable {
			view.PickerNote = picker.UnavailableReason()
		}
	}
	return view, nil
}

// Observe returns nil on native cancellation. The caller rechecks conversation
// revision after this asynchronous operation before publishing the preview.
func (s *FolderObservationService) Observe(ctx context.Context, target foldercontext.Target, mode, chip string) (*foldercontext.Observation, error) {
	if s == nil || s.digest == nil || s.digest.store == nil || !target.Valid() {
		return nil, ErrFolderSelection
	}
	if (mode != "chip" && mode != "picker") || (mode == "picker" && chip != "") {
		return nil, ErrFolderSelection
	}
	d := s.digest
	binding, err := d.store.Binding(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	if binding.HQWorkspaceID != target.WorkspaceID {
		return nil, ErrFolderSelection
	}
	s.mu.Lock()
	s.pruneLocked()
	if s.busy[target.UserID] {
		s.mu.Unlock()
		return nil, ErrFolderScanBusy
	}
	count := 0
	for _, selection := range s.selections {
		if selection.target.UserID == target.UserID {
			count++
		}
	}
	if count >= folderSelectionsPerOwner || len(s.selections) >= folderSelectionsMax {
		s.mu.Unlock()
		return nil, ErrFolderSelectionLimit
	}
	s.busy[target.UserID] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.busy, target.UserID); s.mu.Unlock() }()

	var raw string
	if mode == "chip" {
		raw, err = d.chipPath(chip)
	} else {
		if d.deps.Picker == nil || !d.deps.Picker.Available() {
			return nil, ErrFolderPickerUnavailable
		}
		var chosen bool
		raw, chosen, err = d.deps.Picker.Choose(ctx, folderPickerPrompt)
		if err == nil && !chosen {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	root, err := d.deps.ValidateRoot(raw)
	if err != nil {
		return nil, err
	}
	identity, err := portfolioDirectoryIdentity(root)
	if err != nil {
		return nil, ErrFolderPathLost
	}
	result, err := d.deps.Scan(root)
	if err != nil {
		return nil, &FolderRootError{Message: "Ori could not inspect that folder. Check access or choose a different folder. No file contents were read."}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := d.deps.ValidateRoot(root)
	after, identityErr := portfolioDirectoryIdentity(root)
	fresh, bindingErr := d.store.Binding(ctx, target.UserID)
	if err != nil || canonical != root || identityErr != nil || identity != after || bindingErr != nil || fresh != binding {
		return nil, ErrFolderPathLost
	}
	observation := summarizeFolder(result, d.deps.NewID())
	if err := observation.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	// Other owners can finish scans while this one runs. Enforce the global
	// bound again rather than evicting an unexpired preview unexpectedly.
	if len(s.selections) >= folderSelectionsMax {
		return nil, ErrFolderSelectionLimit
	}
	s.selections[observation.ID] = heldFolderObservation{
		target: target, binding: binding, root: root, identity: identity, result: result, observation: observation,
	}
	return &observation, nil
}

func (s *FolderObservationService) pruneLocked() {
	now := s.digest.now()
	for id, selection := range s.selections {
		if now.Before(selection.observation.ScannedAt) || now.Sub(selection.observation.ScannedAt) >= foldercontext.SelectionTTL {
			delete(s.selections, id)
		}
	}
}

// Resolve accepts only a live, exactly scoped selection. Historical fallback
// belongs to the conversation handler, using its canonical saved snapshot.
func (s *FolderObservationService) Resolve(ctx context.Context, target foldercontext.Target, id string) (*foldercontext.Observation, error) {
	selection, _, err := s.resolve(ctx, target, id)
	if err != nil {
		return nil, err
	}
	observation := selection.observation
	// Copy the slices: consumers cannot mutate the server's held evidence.
	observation.Kinds = append([]foldercontext.Kind(nil), observation.Kinds...)
	observation.Projects = append([]foldercontext.Project(nil), observation.Projects...)
	return &observation, nil
}

func (s *FolderObservationService) resolve(ctx context.Context, target foldercontext.Target, id string) (heldFolderObservation, FolderContinuationReason, error) {
	if s == nil || s.digest == nil || !target.Valid() {
		return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
	}
	s.mu.Lock()
	selection, ok := s.selections[id]
	s.mu.Unlock()
	if !ok || selection.target != target {
		return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
	}
	now := s.digest.now()
	if now.Before(selection.observation.ScannedAt) || now.Sub(selection.observation.ScannedAt) >= foldercontext.SelectionTTL {
		return heldFolderObservation{}, FolderContinuationExpired, ErrFolderSelection
	}
	binding, err := s.digest.store.Binding(ctx, target.UserID)
	if err != nil || binding != selection.binding {
		return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
	}
	canonical, err := s.digest.deps.ValidateRoot(selection.root)
	identity, identityErr := portfolioDirectoryIdentity(selection.root)
	if err != nil || canonical != selection.root || identityErr != nil || identity != selection.identity {
		return heldFolderObservation{}, FolderContinuationChanged, ErrFolderPathLost
	}
	return selection, "", nil
}

// Status never rescans. A saved observation remains history when the live
// selection was lost, expired or replaced. The input must come from storage.
func (s *FolderObservationService) Status(ctx context.Context, target foldercontext.Target, observation foldercontext.Observation) FolderContinuationReason {
	if s == nil || s.digest == nil {
		return FolderContinuationLost
	}
	now := s.digest.now()
	if now.Before(observation.ScannedAt) || now.Sub(observation.ScannedAt) >= foldercontext.SelectionTTL {
		return FolderContinuationExpired
	}
	_, reason, _ := s.resolve(ctx, target, observation.ID)
	return reason
}

// WasSaved checks process-local consumption only, without touching the source.
// The host still requires the exact active canonical snapshot for such IDs.
func (s *FolderObservationService) WasSaved(target foldercontext.Target, id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	selection, ok := s.selections[id]
	return ok && selection.target == target && selection.saved
}

// BindSaved moves exactly one staged selection to its newly saved conversation.
// It does not restore a missing selection, and is called only after persistence.
func (s *FolderObservationService) BindSaved(target foldercontext.Target, id, conversationID string) {
	if s == nil || conversationID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	selection, ok := s.selections[id]
	if !ok || selection.target != target || (target.ConversationID != "" && target.ConversationID != conversationID) {
		return
	}
	selection.target.ConversationID, selection.target.DraftID = conversationID, ""
	selection.saved = true
	s.selections[id] = selection
}

func summarizeFolder(result folderdigest.Result, id string) foldercontext.Observation {
	root := result.RootCandidate()
	observation := foldercontext.Observation{
		Version: foldercontext.Version, ID: id, Folder: foldercontext.DisplayName(result.Name), ScannedAt: result.ScannedAt,
		Entries: result.Entries, Files: root.FileCount,
		Kinds: []foldercontext.Kind{}, Projects: []foldercontext.Project{},
		Coverage: foldercontext.Coverage{MaxDepth: folderdigest.DefaultMaxDepth, MaxEntries: folderdigest.DefaultMaxEntries,
			BudgetSeconds: int(folderdigest.DefaultBudget.Seconds()), Partial: result.Partial, PartialReason: result.PartialReason, SkippedLinks: result.SkippedLinks},
	}
	// The scanner increments once to discover its entry limit. That sentinel
	// was not inspected and is not part of the observed entry count.
	if result.PartialReason == folderdigest.PartialEntries && observation.Entries > 0 {
		observation.Entries--
	}
	if observation.Folder == "" {
		observation.Folder = "Selected folder"
	}
	for extension, count := range root.Extensions {
		name := foldercontext.DisplayName(extension)
		if name == "" {
			name = "No extension"
		}
		observation.Kinds = append(observation.Kinds, foldercontext.Kind{Name: name, Count: count})
	}
	sort.Slice(observation.Kinds, func(i, j int) bool {
		if observation.Kinds[i].Count != observation.Kinds[j].Count {
			return observation.Kinds[i].Count > observation.Kinds[j].Count
		}
		return observation.Kinds[i].Name < observation.Kinds[j].Name
	})
	if len(observation.Kinds) > foldercontext.MaxNames {
		observation.Coverage.KindsOmitted = len(observation.Kinds) - foldercontext.MaxNames
		observation.Kinds = observation.Kinds[:foldercontext.MaxNames]
	}
	// Root plus immediate subfolders, never an arbitrary file inventory.
	for index, candidate := range result.Candidates {
		if len(observation.Projects) >= foldercontext.MaxNames {
			observation.Coverage.ProjectsOmitted++
			continue
		}
		name := foldercontext.DisplayName(candidate.Name)
		if name == "" {
			name = "Unnamed folder"
		}
		project := foldercontext.Project{ID: fmt.Sprintf("candidate-%d", index), Name: name, Files: candidate.FileCount, Root: candidate.IsRoot}
		if candidate.Marker != nil {
			project.Marker = foldercontext.DisplayName(candidate.Marker.Name)
		}
		observation.Projects = append(observation.Projects, project)
	}
	return observation
}
