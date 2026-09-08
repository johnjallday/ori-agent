package samplelibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/specialistevents"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxCopyEntries = 100
const managedCopyLocator = "Samples/Ori Imports"

type CopyPreviewItem struct {
	EntryID            string `json:"entry_id"`
	Filename           string `json:"filename"`
	SizeBytes          int64  `json:"size_bytes"`
	SourcePath         string `json:"source_path"`
	DestinationPath    string `json:"destination_path"`
	DestinationLocator string `json:"destination_locator"`
	CollisionResolved  bool   `json:"collision_resolved"`
}
type CopyReview struct {
	Token                  string            `json:"token"`
	ExpiresAt              time.Time         `json:"expires_at"`
	HomeWorkspaceID        string            `json:"home_workspace_id"`
	ChildWorkspaceID       string            `json:"child_workspace_id"`
	AssistantProjectLinkID string            `json:"assistant_project_link_id"`
	CatalogRevision        int64             `json:"catalog_revision"`
	Items                  []CopyPreviewItem `json:"items"`
	Disclosure             []string          `json:"disclosure"`
}
type CopyResult struct {
	HomeWorkspaceID        string      `json:"home_workspace_id"`
	ChildWorkspaceID       string      `json:"child_workspace_id"`
	AssistantProjectLinkID string      `json:"assistant_project_link_id"`
	Copies                 []ChildCopy `json:"copies"`
	Replayed               bool        `json:"replayed,omitempty"`
}
type stagedCopy struct {
	copy         ChildCopy
	temp, target string
}
type copyPlan struct {
	review                        CopyReview
	inputDigest, disclosureDigest string
	sourcePaths, targetPaths      []string
	entries                       []Entry
}

func (s *Service) ReviewCopy(ctx context.Context, homeID, childID string, entryIDs []string) (CopyReview, error) {
	plan, err := s.buildCopyPlan(ctx, homeID, childID, entryIDs)
	if err != nil {
		return CopyReview{}, err
	}
	now := s.now()
	plan.review.Token = uuid.NewString()
	plan.review.ExpiresAt = now.Add(15 * time.Minute)
	selectionDigest, _ := copySelectionDigest(homeID, childID, entryIDs)
	if err = s.store.CreateCopyReview(ctx, RootReviewRecord{Token: plan.review.Token, HomeWorkspaceID: homeID, SelectionDigest: selectionDigest, InputDigest: plan.inputDigest, DisclosureDigest: plan.disclosureDigest, CatalogRevision: plan.review.CatalogRevision, CreatedAt: now, ExpiresAt: plan.review.ExpiresAt}); err != nil {
		return CopyReview{}, ErrOperationFailed
	}
	return plan.review, nil
}

func (s *Service) CommitCopy(ctx context.Context, homeID, childID, reviewToken, idempotency string, entryIDs []string) (_ CopyResult, resultErr error) {
	defer func() {
		recordSampleFailure(specialistevents.SampleHandoffOutcome, eventActionCopyToProject, resultErr)
	}()
	copyKey := homeID + ":" + childID
	s.mu.Lock()
	if _, busy := s.copying[copyKey]; busy {
		s.mu.Unlock()
		return CopyResult{}, ErrOperationInProgress
	}
	operationCtx, operationCancel := context.WithCancel(ctx)
	s.copying[copyKey] = operationCancel
	s.mu.Unlock()
	defer func() { operationCancel(); s.mu.Lock(); delete(s.copying, copyKey); s.mu.Unlock() }()
	if strings.TrimSpace(reviewToken) == "" || strings.TrimSpace(idempotency) == "" {
		return CopyResult{}, ErrOperationFailed
	}
	review, err := s.store.Review(ctx, reviewToken, "copy")
	if err == nil && review.HomeWorkspaceID != homeID {
		err = ErrNotFound
	}
	if err != nil {
		return CopyResult{}, err
	}
	selectionDigest, selectionErr := copySelectionDigest(homeID, childID, entryIDs)
	if selectionErr != nil || selectionDigest != review.SelectionDigest {
		return CopyResult{}, ErrRevisionConflict
	}
	status, operationErr := s.store.CopyOperationByKey(ctx, homeID, idempotency, review.InputDigest)
	if operationErr != nil {
		return CopyResult{}, operationErr
	}
	if status == "succeeded" {
		return CopyResult{HomeWorkspaceID: homeID, ChildWorkspaceID: childID, Replayed: true}, nil
	}
	if status == "reconcile_required" {
		copies, recoverErr := s.recoverCopies(operationCtx, homeID, childID, idempotency, entryIDs)
		if recoverErr != nil {
			return CopyResult{}, recoverErr
		}
		return CopyResult{HomeWorkspaceID: homeID, ChildWorkspaceID: childID, Copies: copies, Replayed: true}, nil
	}
	if status != "" || review.ConsumedAt != nil {
		return CopyResult{}, ErrIdempotencyConflict
	}
	plan, err := s.buildCopyPlan(ctx, homeID, childID, entryIDs)
	if err != nil {
		return CopyResult{}, err
	}
	if !review.ExpiresAt.After(s.now()) || review.CatalogRevision != plan.review.CatalogRevision || review.InputDigest != plan.inputDigest || review.DisclosureDigest != plan.disclosureDigest {
		return CopyResult{}, ErrRevisionConflict
	}
	staged := make([]stagedCopy, 0, len(plan.entries))
	now := s.now()
	for index, entry := range plan.entries {
		copyID := copyRecordID(homeID, idempotency, entry.ID)
		hash, temp, stageErr := stageExactFile(operationCtx, plan.sourcePaths[index], plan.targetPaths[index], entry, copyID)
		if stageErr != nil {
			removeCopyStages(staged)
			return CopyResult{}, stageErr
		}
		record := ChildCopy{ID: copyID, ChildWorkspaceID: childID, AssistantProjectLinkID: plan.review.AssistantProjectLinkID, SourceRootID: entry.RootID, SourceEntryID: entry.ID, DestinationLocator: plan.review.Items[index].DestinationLocator, SizeBytes: entry.SizeBytes, SHA256: hash, CopiedAt: now}
		staged = append(staged, stagedCopy{copy: record, temp: temp, target: plan.targetPaths[index]})
	}
	if err = s.revalidateCopyAuthorization(ctx, homeID, childID, plan.review.AssistantProjectLinkID, plan.review.CatalogRevision); err != nil {
		removeCopyStages(staged)
		return CopyResult{}, err
	}
	copies := make([]ChildCopy, len(staged))
	for index := range staged {
		copies[index] = staged[index].copy
	}
	if err = s.store.BeginCopies(ctx, homeID, reviewToken, idempotency, plan.inputDigest, copies, now); err != nil {
		removeCopyStages(staged)
		return CopyResult{}, err
	}
	if s.beforeCopyPromote != nil {
		if err = s.beforeCopyPromote(); err != nil {
			return CopyResult{}, ErrOperationFailed
		}
	}
	for _, item := range staged {
		if err = promoteStagedCopy(operationCtx, item.temp, item.target, item.copy); err != nil {
			return CopyResult{}, err
		}
	}
	if err = s.store.CompleteCopies(ctx, homeID, idempotency, s.now()); err != nil {
		return CopyResult{}, err
	}
	recordSampleEvent(specialistevents.SampleHandoffOutcome, eventActionCopyToProject, specialistevents.OutcomeSucceeded, len(copies))
	return CopyResult{HomeWorkspaceID: homeID, ChildWorkspaceID: childID, AssistantProjectLinkID: plan.review.AssistantProjectLinkID, Copies: copies}, nil
}

func copyRecordID(homeID, key, entryID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join([]string{homeID, key, entryID}, "\x00"))).String()
}
func (s *Service) recoverCopies(ctx context.Context, homeID, childID, key string, entryIDs []string) ([]ChildCopy, error) {
	ids, err := normalizeCopyIDs(entryIDs)
	if err != nil {
		return nil, err
	}
	copyIDs := make([]string, len(ids))
	for i, id := range ids {
		copyIDs[i] = copyRecordID(homeID, key, id)
	}
	copies, err := s.store.CopiesByIDs(ctx, copyIDs)
	if err != nil || len(copies) != len(ids) {
		return nil, ErrOperationFailed
	}
	projectRoot, err := s.childProjectRoot(childID)
	if err != nil {
		return nil, err
	}
	for _, copy := range copies {
		if copy.ChildWorkspaceID != childID {
			return nil, ErrRevisionConflict
		}
		target, resolveErr := managedCopyTarget(projectRoot, copy.DestinationLocator)
		if resolveErr != nil {
			return nil, resolveErr
		}
		temp := filepath.Join(filepath.Dir(target), ".ori-copy-"+copy.ID+".stage")
		if err = promoteStagedCopy(ctx, temp, target, copy); err != nil {
			return nil, err
		}
	}
	if err = s.store.CompleteCopies(ctx, homeID, key, s.now()); err != nil {
		return nil, err
	}
	return copies, nil
}
func (s *Service) childProjectRoot(childID string) (string, error) {
	child, err := s.workspaces.Get(childID)
	if err != nil {
		return "", ErrOperationFailed
	}
	folderStore, ok := s.workspaces.(interface{ GetFolderPath(string) (string, error) })
	if !ok {
		return "", ErrOperationFailed
	}
	workspaceRoot, err := folderStore.GetFolderPath(child.ID)
	if err != nil {
		return "", ErrOperationFailed
	}
	entry, err := workspace.ResolveProjectEntry(child, workspaceRoot)
	if err != nil {
		return "", ErrOperationFailed
	}
	return filepath.Dir(entry.AbsolutePath), nil
}
func managedCopyTarget(projectRoot, locator string) (string, error) {
	prefix := managedCopyLocator + "/"
	if !strings.HasPrefix(locator, prefix) || strings.Contains(locator, "\\") {
		return "", ErrOperationFailed
	}
	name := strings.TrimPrefix(locator, prefix)
	if filepath.Base(name) != name || name == "" {
		return "", ErrOperationFailed
	}
	root, err := ensureManagedCopyDirectory(projectRoot, true)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

func (s *Service) revalidateCopyAuthorization(ctx context.Context, homeID, childID, linkID string, catalogRevision int64) error {
	state, err := s.store.Get(ctx, homeID)
	if err != nil || state.CatalogRevision != catalogRevision {
		return ErrRevisionConflict
	}
	home, err := s.workspaces.Get(homeID)
	if err != nil {
		return ErrOperationFailed
	}
	program := home.GetAssistantProgramState()
	child, err := s.workspaces.Get(childID)
	if err != nil {
		return ErrOperationFailed
	}
	link := child.GetAssistantProjectLink()
	if program == nil || link == nil || link.ID != linkID || link.StationWorkspaceID != homeID || link.Key.Normalize() != program.Key.Normalize() || !containsString(program.LinkedProjectIDs, childID) {
		return ErrRevisionConflict
	}
	return nil
}

func (s *Service) buildCopyPlan(ctx context.Context, homeID, childID string, entryIDs []string) (copyPlan, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return copyPlan{}, err
	}
	ids, err := normalizeCopyIDs(entryIDs)
	if err != nil {
		return copyPlan{}, err
	}
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return copyPlan{}, err
	}
	home, err := s.workspaces.Get(homeID)
	if err != nil {
		return copyPlan{}, ErrOperationFailed
	}
	program := home.GetAssistantProgramState()
	child, err := s.workspaces.Get(strings.TrimSpace(childID))
	if err != nil {
		return copyPlan{}, ErrOperationFailed
	}
	link := child.GetAssistantProjectLink()
	if program == nil || link == nil || link.StationWorkspaceID != homeID || link.Key.Normalize() != program.Key.Normalize() || link.ID != workspace.AssistantProjectLinkID(homeID, child.ID) || !containsString(program.LinkedProjectIDs, child.ID) {
		return copyPlan{}, ErrOperationFailed
	}
	folderStore, ok := s.workspaces.(interface{ GetFolderPath(string) (string, error) })
	if !ok {
		return copyPlan{}, ErrOperationFailed
	}
	workspaceRoot, err := folderStore.GetFolderPath(child.ID)
	if err != nil {
		return copyPlan{}, ErrOperationFailed
	}
	projectEntry, err := workspace.ResolveProjectEntry(child, workspaceRoot)
	if err != nil {
		return copyPlan{}, ErrOperationFailed
	}
	projectRoot := filepath.Dir(projectEntry.AbsolutePath)
	destinationRoot, err := ensureManagedCopyDirectory(projectRoot, false)
	if err != nil {
		return copyPlan{}, err
	}
	reserved := map[string]bool{}
	plan := copyPlan{review: CopyReview{HomeWorkspaceID: homeID, ChildWorkspaceID: child.ID, AssistantProjectLinkID: link.ID, CatalogRevision: state.CatalogRevision, Disclosure: []string{"Copies only the selected catalog files.", "Does not change source files or grant the project access to library folders.", "Does not insert media into a project file."}}}
	digestParts := []string{homeID, child.ID, link.ID, fmt.Sprint(link.StateRevision), fmt.Sprint(state.CatalogRevision)}
	disclosureParts := append([]string(nil), plan.review.Disclosure...)
	var totalBytes int64
	for _, id := range ids {
		entry, getErr := s.store.ActiveEntry(ctx, homeID, id)
		if getErr != nil {
			return copyPlan{}, ErrNotFound
		}
		root, getErr := s.store.Root(ctx, homeID, entry.RootID)
		if getErr != nil || root.Generation != entry.Generation {
			return copyPlan{}, ErrRootChanged
		}
		if entry.SizeBytes < 0 || entry.SizeBytes > MaxHashFileBytes || totalBytes > MaxHashTotalBytes-entry.SizeBytes {
			return copyPlan{}, ErrOperationFailed
		}
		totalBytes += entry.SizeBytes
		wsRef, getErr := home.GetDirectoryReference(root.DirectoryReferenceID)
		if getErr != nil || wsRef.Purpose != "sample_library" {
			return copyPlan{}, ErrRootMissing
		}
		canonical, fingerprint, getErr := s.validateRoot(ctx, wsRef.Path, root.ID)
		if getErr != nil || fingerprint != root.DirectoryFingerprint {
			return copyPlan{}, ErrRootChanged
		}
		source, getErr := containedCopySource(canonical, entry.RelativeLocator)
		if getErr != nil {
			return copyPlan{}, ErrRootChanged
		}
		info, getErr := os.Lstat(source)
		if getErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != entry.SizeBytes || !info.ModTime().UTC().Equal(entry.ModifiedAt.UTC()) {
			return copyPlan{}, ErrRootChanged
		}
		name, collision, getErr := availableCopyName(destinationRoot, entry.Filename, reserved)
		if getErr != nil {
			return copyPlan{}, getErr
		}
		reserved[name] = true
		target := filepath.Join(destinationRoot, name)
		locator := filepath.ToSlash(filepath.Join(managedCopyLocator, name))
		item := CopyPreviewItem{EntryID: entry.ID, Filename: entry.Filename, SizeBytes: entry.SizeBytes, SourcePath: source, DestinationPath: target, DestinationLocator: locator, CollisionResolved: collision}
		plan.entries = append(plan.entries, entry)
		plan.sourcePaths = append(plan.sourcePaths, source)
		plan.targetPaths = append(plan.targetPaths, target)
		plan.review.Items = append(plan.review.Items, item)
		digestParts = append(digestParts, entry.ID, entry.RootID, entry.RelativeLocator, fmt.Sprint(entry.SizeBytes), entry.ModifiedAt.UTC().Format(time.RFC3339Nano), locator)
		disclosureParts = append(disclosureParts, source, target)
	}
	plan.inputDigest = digestStrings(digestParts...)
	plan.disclosureDigest = digestStrings(disclosureParts...)
	return plan, nil
}

func containedCopySource(root, locator string) (string, error) {
	if locator == "" || filepath.IsAbs(locator) || strings.Contains(locator, "\\") {
		return "", ErrRootChanged
	}
	clean := filepath.Clean(filepath.FromSlash(locator))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrRootChanged
	}
	target := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrRootChanged
	}
	return target, nil
}

func copySelectionDigest(homeID, childID string, values []string) (string, error) {
	ids, err := normalizeCopyIDs(values)
	if err != nil {
		return "", err
	}
	return digestStrings(append([]string{homeID, strings.TrimSpace(childID)}, ids...)...), nil
}

func normalizeCopyIDs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxCopyEntries {
		return nil, ErrOperationFailed
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" || len(id) > 160 || seen[id] {
			return nil, ErrOperationFailed
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func availableCopyName(root, name string, reserved map[string]bool) (string, bool, error) {
	clean := filepath.Base(strings.TrimSpace(name))
	if clean == "" || clean == "." || clean != name {
		return "", false, ErrOperationFailed
	}
	extension := filepath.Ext(clean)
	stem := strings.TrimSuffix(clean, extension)
	for attempt := 1; attempt <= 1000; attempt++ {
		candidate := clean
		if attempt > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, attempt, extension)
		}
		if reserved[candidate] {
			continue
		}
		_, err := os.Lstat(filepath.Join(root, candidate))
		if errors.Is(err, os.ErrNotExist) {
			return candidate, attempt > 1, nil
		}
		if err != nil {
			return "", false, ErrOperationFailed
		}
	}
	return "", false, ErrOperationFailed
}
func ensureManagedCopyDirectory(projectRoot string, create bool) (string, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", ErrOperationFailed
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrOperationFailed
	}
	current := root
	for _, part := range []string{"Samples", "Ori Imports"} {
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if create {
				if err = os.Mkdir(current, 0750); err != nil && !os.IsExist(err) {
					return "", ErrOperationFailed
				}
				info, err = os.Lstat(current)
			} else {
				continue
			}
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", ErrOperationFailed
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", ErrOperationFailed
		}
	}
	return current, nil
}
func stageExactFile(ctx context.Context, source, target string, expected Entry, copyID string) (string, string, error) {
	if _, err := ensureManagedCopyDirectory(filepath.Dir(filepath.Dir(filepath.Dir(target))), true); err != nil {
		return "", "", err
	}
	sourceFile, err := openReadNoFollow(source)
	if err != nil {
		return "", "", ErrRootChanged
	}
	defer func() { _ = sourceFile.Close() }()
	before, err := sourceFile.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != expected.SizeBytes || !before.ModTime().UTC().Equal(expected.ModifiedAt.UTC()) {
		return "", "", ErrRootChanged
	}
	temp := filepath.Join(filepath.Dir(target), ".ori-copy-"+copyID+".stage")
	_ = os.Remove(temp)
	output, err := openWriteNoFollow(temp)
	if err != nil {
		return "", "", ErrOperationFailed
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temp)
		}
	}()
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(&contextReader{ctx: ctx, reader: sourceFile}, expected.SizeBytes+1))
	syncErr := output.Sync()
	closeErr := output.Close()
	after, statErr := sourceFile.Stat()
	if copyErr != nil || syncErr != nil || closeErr != nil || statErr != nil || written != expected.SizeBytes || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", "", ErrOperationFailed
	}
	keep = true
	return hex.EncodeToString(hash.Sum(nil)), temp, nil
}
func promoteStagedCopy(ctx context.Context, temp, target string, copy ChildCopy) error {
	select {
	case <-ctx.Done():
		return ErrOperationFailed
	default:
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != copy.SizeBytes {
			return ErrOperationFailed
		}
		hash, hashErr := hashStagedFile(ctx, target, copy.SizeBytes)
		if hashErr != nil || hash != copy.SHA256 {
			return ErrOperationFailed
		}
		_ = os.Remove(temp)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrOperationFailed
	}
	hash, err := hashStagedFile(ctx, temp, copy.SizeBytes)
	if err != nil || hash != copy.SHA256 {
		return ErrOperationFailed
	}
	if err = os.Link(temp, target); err != nil {
		return ErrOperationFailed
	}
	if err = os.Remove(temp); err != nil {
		_ = os.Remove(target)
		return ErrOperationFailed
	}
	return nil
}
func hashStagedFile(ctx context.Context, path string, size int64) (string, error) {
	file, err := openReadNoFollow(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return "", ErrOperationFailed
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: file}, size+1))
	if err != nil || written != size {
		return "", ErrOperationFailed
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func removeCopyStages(values []stagedCopy) {
	for _, value := range values {
		_ = os.Remove(value.temp)
	}
}
