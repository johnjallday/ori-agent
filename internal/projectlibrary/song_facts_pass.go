package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The fact pass reads song facts after a consenting Home's scan is recorded.
// It never changes the listing: it opens only the file that dates each song,
// through the scan's pinned no-follow walk, re-checks that the file is the one
// the listing saw, reads three numbers and saves them in a second fenced
// write. A song it does not reach keeps no facts until a later scan.

// songFactsBudget bounds one scan's pass: wall time and bytes read (T2).
type songFactsBudget struct {
	Duration time.Duration
	Bytes    int64
}

var defaultSongFactsBudget = songFactsBudget{Duration: 10 * time.Second, Bytes: 512 << 20}

// songFactsBatch is how many files are read per hold of the root gate, so a
// revocation waits for at most one batch and each batch is bracketed by a
// fresh check of the root and the consent.
const songFactsBatch = 32

// songFactsTarget is one song whose dated file the pass may read.
type songFactsTarget struct {
	Relative       string      `json:"relative_folder"`
	FolderIdentity string      `json:"folder_identity"`
	Format         string      `json:"format"`
	File           FactsSource `json:"file"`
}

// songFactsRead is one file the pass read, with what it found. Empty facts
// mean the content gave none; the file is not read again until it changes.
type songFactsRead struct {
	songFactsTarget
	Facts SongFacts `json:"facts"`
}

// songFactsOutcome is what one pass did, for the log and tests. Counts only.
type songFactsOutcome struct {
	Targets, Opened, Read, Skipped int
	Bytes                          int64
	Stopped                        string // "", "time", "bytes", "cancelled", "authority"
	Saved                          int
}

func (r *Roots) songFactsBudgetOrDefault() songFactsBudget {
	if r.factsBudget.Duration > 0 && r.factsBudget.Bytes > 0 {
		return r.factsBudget
	}
	return defaultSongFactsBudget
}

// readSongFactsAfterScan runs the pass for a consenting Home and saves what it
// read. Every failure leaves the listing and older facts as they are; the
// caller publishes the scan either way.
func (r *Roots) readSongFactsAfterScan(ctx context.Context, scope Scope, scan Scan, observed Discovery, root Root) songFactsOutcome {
	targets, ok := r.songFactsTargets(scope, scan, observed)
	if !ok || len(targets) == 0 {
		return songFactsOutcome{}
	}
	started := time.Now()
	reads, outcome := r.songFactsPass(ctx, scope, root, targets)
	if len(reads) > 0 {
		saved, err := r.saveSongFacts(scope, scan, reads)
		if err != nil {
			logger.Warn("Project library could not save song facts", logger.Fields{"home_id": scope.HomeID,
				"scan_id": scan.ID, "error": err.Error()})
		}
		outcome.Saved = saved
	}
	logger.Info("Project library read song facts", logger.Fields{"home_id": scope.HomeID, "scan_id": scan.ID,
		"targets": outcome.Targets, "opened": outcome.Opened, "read": outcome.Read, "skipped": outcome.Skipped,
		"saved": outcome.Saved, "bytes": outcome.Bytes, "stopped": outcome.Stopped,
		"elapsed_ms": time.Since(started).Milliseconds()})
	return outcome
}

// songFactsTargets lists this scan's songs of a facts-declaring format that
// have no current facts, most recently saved first. A Home without an active
// consent has none: the pass opens nothing for it.
func (r *Roots) songFactsTargets(scope Scope, scan Scan, observed Discovery) ([]songFactsTarget, bool) {
	doc, state, err := r.library.readSnapshot(scope)
	if err != nil || !state.GetSongDetailsConsent().Active() {
		return nil, false
	}
	var targets []songFactsTarget
	for _, candidate := range observed.Candidates {
		if candidate.DatedFile == nil || !factsFile(candidate.Format, candidate.DatedFile.File) {
			continue
		}
		observation := scanObservation(doc, scan, candidate.RelativeFolder, candidate.FileIdentity)
		if observation == nil || observation.Facts != nil || !factsCanAttach(*observation, *candidate.DatedFile) {
			continue
		}
		targets = append(targets, songFactsTarget{Relative: candidate.RelativeFolder,
			FolderIdentity: candidate.FileIdentity, Format: candidate.Format, File: *candidate.DatedFile})
	}
	sort.SliceStable(targets, func(i, j int) bool {
		return targets[i].File.ModifiedAt.After(targets[j].File.ModifiedAt)
	})
	return targets, true
}

// scanObservation is the observation this scan recorded for one folder, or nil.
func scanObservation(doc Document, scan Scan, relative, identity string) *Observation {
	for i := range doc.Entries {
		for j := range doc.Entries[i].Observations {
			o := &doc.Entries[i].Observations[j]
			if o.RootID == scan.RootID && o.RelativeFolder == relative && o.FileIdentity == identity && o.ScanID == scan.ID {
				return o
			}
		}
	}
	return nil
}

// factsCanAttach says facts read from file belong on this observation: a usable
// source of a facts-declaring format whose listing named that file and dated
// the song by it.
func factsCanAttach(o Observation, file FactsSource) bool {
	return factsFile(o.Format, file.File) && (o.Availability == "available" || o.Availability == "ambiguous") &&
		slices.Contains(o.Alternates, file.File) && o.FileModifiedAt.Equal(file.ModifiedAt)
}

// songFactsPass reads the targets in batches under the root gate. A batch is
// kept only when the root and consent still hold after it.
func (r *Roots) songFactsPass(ctx context.Context, scope Scope, root Root, targets []songFactsTarget) ([]songFactsRead, songFactsOutcome) {
	budget := r.songFactsBudgetOrDefault()
	outcome := songFactsOutcome{Targets: len(targets)}
	passCtx, cancel := context.WithTimeout(ctx, budget.Duration)
	defer cancel()
	var reads []songFactsRead
	for start := 0; start < len(targets) && outcome.Stopped == ""; start += songFactsBatch {
		end := min(start+songFactsBatch, len(targets))
		batch, stopped := r.songFactsBatch(passCtx, ctx, scope, root, targets[start:end], budget, &outcome)
		reads = append(reads, batch...)
		outcome.Stopped = stopped
	}
	return reads, outcome
}

func (r *Roots) songFactsBatch(passCtx, ctx context.Context, scope Scope, root Root, targets []songFactsTarget,
	budget songFactsBudget, outcome *songFactsOutcome) ([]songFactsRead, string) {
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	if !r.songFactsAuthorityNoGate(scope, root) {
		return nil, "authority"
	}
	var reads []songFactsRead
	stopped := ""
	for _, target := range targets {
		if stopped = songFactsStop(passCtx, ctx); stopped != "" {
			break
		}
		if target.File.Size > DefaultSongFactsLimits.MaxBytes {
			outcome.Skipped++ // Over the cap: never opened, never facts.
			continue
		}
		if outcome.Bytes+target.File.Size > budget.Bytes {
			stopped = "bytes"
			break
		}
		read, ok := r.readOneSongFacts(root, target, outcome)
		if !ok {
			outcome.Skipped++
			continue
		}
		reads = append(reads, read)
	}
	// Results from a root revoked, swapped or switched off during the batch
	// are dropped, as readDirectoryWithIdentity drops its rows.
	if !r.songFactsAuthorityNoGate(scope, root) {
		return nil, "authority"
	}
	return reads, stopped
}

func songFactsStop(passCtx, ctx context.Context) string {
	switch {
	case ctx.Err() != nil:
		return "cancelled"
	case passCtx.Err() != nil:
		return "time"
	}
	return ""
}

// readOneSongFacts opens one dated file, reads it within the per-file caps and
// re-checks it afterwards. An I/O failure or a changed file is skipped.
func (r *Roots) readOneSongFacts(root Root, target songFactsTarget, outcome *songFactsOutcome) (songFactsRead, bool) {
	if r.factsOpened != nil {
		r.factsOpened(target.Relative, target.File.File)
	}
	file, err := openPinnedProjectFile(root, target.Relative, target.FolderIdentity, target.File)
	if err != nil {
		return songFactsRead{}, false
	}
	defer func() { _ = file.Close() }()
	outcome.Opened++
	outcome.Bytes += target.File.Size
	facts, err := ReadSongFacts(file, factsGrammar(target.Format), DefaultSongFactsLimits)
	if err != nil && !errors.Is(err, ErrSongFactsUnreadable) {
		return songFactsRead{}, false
	}
	if !pinnedFileStillMatches(file, target.File) {
		return songFactsRead{}, false // re-saved while it was read
	}
	outcome.Read++
	return songFactsRead{songFactsTarget: target, Facts: facts}, true
}

// songFactsAuthorityNoGate is the pass's grant: the root is the one the scan
// used and still connected, and the Home's switch is on. The caller holds the
// root gate.
func (r *Roots) songFactsAuthorityNoGate(scope Scope, root Root) bool {
	live, state, err := r.verifyConnectedRootStateNoGate(scope, root.ID)
	return err == nil && live.Revision == root.Revision && live.Path == root.Path &&
		live.FileIdentity == root.FileIdentity && state.GetSongDetailsConsent().Active()
}

var errNoFactsChange = errors.New("project library song facts unchanged")

// saveSongFacts writes the facts in one fenced Home write keyed by the scan, so
// a retry is a replay. A Home whose switch went off, or whose observations moved
// on to a newer scan, takes none of them.
func (r *Roots) saveSongFacts(scope Scope, scan Scan, reads []songFactsRead) (int, error) {
	payload, err := json.Marshal(reads)
	if err != nil {
		return 0, ErrCorrupt
	}
	sum := sha256.Sum256(payload)
	op := operation{key: scan.ID + ":facts", action: "scan_facts", digest: hex.EncodeToString(sum[:])}
	var saved int
	for attempt := 0; attempt < 3; attempt++ {
		doc, err := r.library.Read(scope)
		if err != nil {
			return 0, err
		}
		_, replay, err := r.library.mutateWithHomePolicy(scope, doc.Revision, op,
			func(state *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
				return state.GetSongDetailsConsent().Active()
			}, func(current *Document) (string, error) {
				saved = 0
				for _, read := range reads {
					if attachSongFacts(current, scan, read) {
						saved++
					}
				}
				if saved == 0 {
					return "", errNoFactsChange
				}
				return scan.ID, nil
			})
		switch {
		case errors.Is(err, errNoFactsChange):
			return 0, nil
		case errors.Is(err, ErrConflict) && !replay:
			continue // another write landed first; reapply on the fresh document
		case err != nil:
			return 0, err
		case replay:
			return 0, nil
		}
		return saved, nil
	}
	return 0, ErrConflict
}

// attachSongFacts sets one song's facts on the observation this scan recorded,
// only while it still has none and still names the file they were read from.
func attachSongFacts(doc *Document, scan Scan, read songFactsRead) bool {
	for i := range doc.Entries {
		for j := range doc.Entries[i].Observations {
			o := &doc.Entries[i].Observations[j]
			if o.RootID != scan.RootID || o.RelativeFolder != read.Relative || o.FileIdentity != read.FolderIdentity {
				continue
			}
			if o.ScanID != scan.ID || o.Facts != nil || !factsCanAttach(*o, read.File) {
				return false
			}
			o.Facts = &ObservedFacts{SongFacts: read.Facts, ReadFrom: read.File}
			return true
		}
	}
	return false
}
