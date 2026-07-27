package downloadsjanitor

import (
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// SettleInterval is how long a file's size and modification time must hold
// steady, across two separate observations, before it is considered a finished
// download (FR-32). A file that is still changing is left alone and retried on a
// later scan.
const SettleInterval = 30 * time.Second

// ErrRootUnavailable reports that the configured folder could not be resolved
// or read on this run. It is recoverable: readiness reports it and the user
// relinks or restores the folder.
var ErrRootUnavailable = errors.New("downloads janitor folder is unavailable")

// Scanner enumerates one workspace's configured folder and turns eligible,
// settled files into candidates.
//
// Everything it does is metadata-only: it stats directory entries and reads
// names. No file is ever opened.
type Scanner struct {
	store      *Store
	workspaces WorkspaceStore
	now        func() time.Time
}

// NewScanner builds a scanner over the Janitor's own state store and the
// workspace store that owns directory references.
func NewScanner(store *Store, workspaces WorkspaceStore) *Scanner {
	return &Scanner{store: store, workspaces: workspaces, now: time.Now}
}

func (s *Scanner) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// ScanResult is one enumeration's outcome, before anything is persisted.
type ScanResult struct {
	// Eligible are the files that would become candidates: top-level regular
	// files, not filtered, settled, and not already proposed or skipped.
	Eligible []JanitorCandidate
	// Ineligible explains every file the scan looked at and passed over.
	Ineligible []IneligibleObservation
	// ScannedAt is when the enumeration ran.
	ScannedAt time.Time
	// Root is the folder that was scanned, for the caller's logging. It is not
	// returned to clients.
	Root string
}

// ResolveRoot returns the folder to scan, resolved through the workspace's
// directory reference on every run rather than trusted from settings alone.
//
// The reference is the record of what the user approved. If it has been removed
// or repointed elsewhere, scanning stops instead of quietly following it: a
// folder Ori was granted is not the same as a folder Ori happens to find a path
// to.
func (s *Scanner) ResolveRoot(settings JanitorSettings) (string, error) {
	configured := strings.TrimSpace(settings.RootPath)
	if configured == "" || strings.TrimSpace(settings.DirectoryReferenceID) == "" {
		return "", fmt.Errorf("%w: setup is not complete", ErrRootUnavailable)
	}
	if s.workspaces == nil {
		return "", fmt.Errorf("%w: workspace storage is unavailable", ErrRootUnavailable)
	}
	// Read the canonical folder record when the store can reach it, matching
	// how the service reads workspaces. Directory references do have a SQLite
	// column today, so either source works — but the two paths agreeing on
	// where a workspace's truth lives is what keeps a future field from being
	// silently absent in one of them.
	ws, err := readWorkspaceRecord(s.workspaces, settings.WorkspaceID)
	if err != nil || ws == nil {
		return "", fmt.Errorf("%w: workspace could not be loaded", ErrRootUnavailable)
	}
	var reference *workspace.DirectoryReference
	for i := range ws.DirectoryReferences {
		if ws.DirectoryReferences[i].ID == settings.DirectoryReferenceID {
			reference = &ws.DirectoryReferences[i]
			break
		}
	}
	if reference == nil {
		return "", fmt.Errorf("%w: this workspace is no longer linked to the folder", ErrRootUnavailable)
	}
	resolved := filepath.Clean(strings.TrimSpace(reference.Path))
	if resolved != filepath.Clean(configured) {
		// The approved folder and the linked folder disagree. Acting on either
		// would be acting on something the user did not confirm.
		return "", fmt.Errorf("%w: the linked folder no longer matches the folder you approved", ErrRootUnavailable)
	}
	return resolved, nil
}

// Scan enumerates the configured folder's immediate children and classifies
// each entry as eligible or not. It persists nothing; callers decide whether the
// result becomes a batch (a manual or automatic scan) or is only reported (a
// test scan).
func (s *Scanner) Scan(settings JanitorSettings, state ScanState, source ScanSource) (ScanResult, error) {
	root, err := s.ResolveRoot(settings)
	if err != nil {
		return ScanResult{}, err
	}
	now := s.clock()
	result := ScanResult{ScannedAt: now, Root: root}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsPermission(err) {
			return ScanResult{}, fmt.Errorf("%w: permission denied", ErrRootUnavailable)
		}
		if os.IsNotExist(err) {
			return ScanResult{}, fmt.Errorf("%w: the folder no longer exists", ErrRootUnavailable)
		}
		return ScanResult{}, fmt.Errorf("%w: %v", ErrRootUnavailable, err)
	}

	filingRoot := strings.TrimSpace(settings.FilingRootName)
	if filingRoot == "" {
		filingRoot = DefaultFilingRootName
	}
	active := state.ActiveFingerprints()

	for _, entry := range entries {
		name := entry.Name()
		candidate, reason, ok := s.evaluate(root, filingRoot, name, state, active, source, now)
		if !ok {
			result.Ineligible = append(result.Ineligible, IneligibleObservation{Name: safeName(name), Reason: reason})
			continue
		}
		result.Eligible = append(result.Eligible, candidate)
	}

	sort.SliceStable(result.Eligible, func(i, j int) bool { return result.Eligible[i].Name < result.Eligible[j].Name })
	sort.SliceStable(result.Ineligible, func(i, j int) bool { return result.Ineligible[i].Name < result.Ineligible[j].Name })
	return result, nil
}

// evaluate decides whether one directory entry becomes a candidate.
//
// The checks are ordered cheapest-and-most-certain first, and every rejection
// names a reason: a scan that silently drops files is indistinguishable from a
// scan that is broken.
func (s *Scanner) evaluate(
	root, filingRoot, name string,
	state ScanState,
	active map[string]struct{},
	source ScanSource,
	now time.Time,
) (JanitorCandidate, IneligibleReason, bool) {
	// The filing destination and everything under it is Ori's own output, never
	// input (FR-31). Because the scan is non-recursive, excluding the directory
	// excludes its contents.
	if strings.EqualFold(name, filingRoot) {
		return JanitorCandidate{}, IneligibleInFiledFolder, false
	}
	// Hidden, temporary, backup, and partial-download names are filtered by the
	// same rules the watcher uses, so a file cannot become actionable just
	// because a different code path found it (FR-29, FR-30).
	if filewatcher.ShouldIgnoreFile(name) {
		if filewatcher.IsPartialDownload(name) {
			return JanitorCandidate{}, IneligiblePartial, false
		}
		if strings.HasPrefix(name, ".") {
			return JanitorCandidate{}, IneligibleHidden, false
		}
		return JanitorCandidate{}, IneligibleTemporary, false
	}
	// A name that is not a plain top-level filename cannot be a candidate; the
	// model forbids it, and this is where that is enforced. The name is checked,
	// never rewritten: Ori has to address the file by the name it actually has.
	if err := ValidateFileName(name); err != nil {
		return JanitorCandidate{}, IneligibleUnreadable, false
	}

	// Lstat, not Stat: a symlink must be recognized as a symlink rather than
	// followed to whatever it points at, which could be anywhere (FR-28).
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		return JanitorCandidate{}, IneligibleUnreadable, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return JanitorCandidate{}, IneligibleSymlink, false
	}
	if info.IsDir() {
		return JanitorCandidate{}, IneligibleNotRegularFile, false
	}
	// Sockets, devices, pipes, and anything else that is not a regular file are
	// out: they are not downloads, and reading their metadata is meaningless.
	if !info.Mode().IsRegular() {
		return JanitorCandidate{}, IneligibleNotRegularFile, false
	}

	fingerprint := fingerprintFor(name, info)

	// A file state the user already dismissed stays dismissed (FR-41).
	if state.IsSkipped(fingerprint) {
		return JanitorCandidate{}, IneligibleSkippedByUser, false
	}
	// A file already awaiting the user's decision is not proposed twice, no
	// matter which kind of scan finds it (FR-40).
	if _, exists := active[fingerprint.Key()]; exists {
		return JanitorCandidate{}, IneligibleAlreadyKnown, false
	}

	// Settling is checked last because it is the only check that depends on a
	// previous run.
	if !settled(state, name, info.Size(), info.ModTime(), now) {
		return JanitorCandidate{}, IneligibleUnsettled, false
	}

	candidate := JanitorCandidate{
		WorkspaceID:  state.WorkspaceID,
		Name:         name,
		DisplayName:  DisplayFileName(name),
		Extension:    strings.ToLower(filepath.Ext(name)),
		Size:         info.Size(),
		ModifiedAt:   info.ModTime(),
		DiscoveredAt: now,
		Fingerprint:  fingerprint,
		ScanSource:   source,
		State:        CandidatePending,
	}
	candidate.MIMEType = detectMIMEType(candidate.Extension)
	return candidate, "", true
}

// settled reports whether a file has demonstrably stopped changing.
//
// There are two ways a file can demonstrate that, and which one applies depends
// on whether Ori has ever watched this file change:
//
//   - Ori has witnessed a change (the file grew, or its timestamp moved, while
//     Ori was tracking it). Then only Ori's own observations count: the same
//     size and modification time across two sightings at least the settle
//     interval apart. A writer that stalls mid-file leaves an ageing timestamp
//     that would otherwise look convincing, so the timestamp is not accepted
//     as evidence here.
//   - Ori has never witnessed a change. Then the file's own modification time
//     is enough: a file last written more than the settle interval ago has not
//     been written to since before this scan. This is what lets the first scan
//     of a pre-existing backlog propose files immediately, rather than telling
//     the user to come back later about downloads that have sat untouched for
//     weeks.
//
// A file actively being written satisfies neither: its modification time keeps
// moving, which both fails the unchanged check and marks the change as
// witnessed (see RecordObservation).
func settled(state ScanState, name string, size int64, modTime, now time.Time) bool {
	observation, tracked := state.Observation(name)
	if !tracked {
		return now.Sub(modTime) >= SettleInterval
	}
	if !observation.Unchanged(size, modTime) {
		return false
	}
	if now.Sub(observation.FirstSeenAt) >= SettleInterval {
		return true
	}
	return !observation.ChangeWitnessed && now.Sub(modTime) >= SettleInterval
}

// ObserveForSettling records a sighting of every eligible-shaped entry so the
// next scan can tell whether it has stopped changing. It is separate from Scan
// because observing is a write and scanning is not: a test scan observes
// nothing.
func (s *Scanner) ObserveForSettling(settings JanitorSettings, state *ScanState) error {
	root, err := s.ResolveRoot(settings)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRootUnavailable, err)
	}
	filingRoot := strings.TrimSpace(settings.FilingRootName)
	if filingRoot == "" {
		filingRoot = DefaultFilingRootName
	}
	now := s.clock()
	for _, entry := range entries {
		name := entry.Name()
		if strings.EqualFold(name, filingRoot) || filewatcher.ShouldIgnoreFile(name) {
			continue
		}
		if err := ValidateFileName(name); err != nil {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		RecordObservation(state, name, info.Size(), info.ModTime(), now)
	}
	return nil
}

// fingerprintFor builds the identity a proposal is bound to. It uses the
// platform's file identity when one is available, which is what distinguishes a
// file replaced in place from the file that was approved.
func fingerprintFor(name string, info os.FileInfo) Fingerprint {
	return Fingerprint{
		Name:    name,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		FileID:  platformFileID(info),
	}
}

// detectMIMEType maps an extension to a type without opening the file. An
// unknown extension yields an empty type, which the classifier treats as
// unrecognized rather than guessing.
func detectMIMEType(extension string) string {
	if extension == "" {
		return ""
	}
	detected := mime.TypeByExtension(extension)
	if detected == "" {
		return ""
	}
	if idx := strings.Index(detected, ";"); idx > 0 {
		detected = detected[:idx]
	}
	return strings.TrimSpace(detected)
}

// safeName renders a name for reporting. An ineligible observation must never
// be the thing that puts a control character into a log line.
func safeName(name string) string {
	return DisplayFileName(name)
}
