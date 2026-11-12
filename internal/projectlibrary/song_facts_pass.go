package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
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
// Names are set only for a read made under a consent that covers names.
type songFactsRead struct {
	songFactsTarget
	Facts SongFacts  `json:"facts"`
	Names *SongNames `json:"names,omitempty"`
	// NotRead says why the file gave nothing (NotReadTooLarge, NotReadUnreadable):
	// it is recorded, with the file's identity, so the same unchanged file is not
	// read again.
	NotRead string `json:"not_read,omitempty"`
}

// songFactsGrant is what the Home's consent lets the pass read right now: the
// three numbers under any active consent, and track names, markers and regions
// only when the consent also covers names. It is the one value every read goes
// through: the pass takes it from the Home, the grammar it hands the reader is
// chosen from it, and every later check compares the Home's live grant with it.
type songFactsGrant struct{ names bool }

// songFactsGrantOf is the grant a Home's state gives, and false when the Home
// holds no active consent (the pass then reads and writes nothing).
func songFactsGrantOf(state *workspace.AssistantProgramState) (songFactsGrant, bool) {
	consent := state.GetSongDetailsConsent()
	if !consent.Active() {
		return songFactsGrant{}, false
	}
	return songFactsGrant{names: consent.CoversNames()}, true
}

// covers says g allows everything other does: a pass that began under a names
// grant is dropped if the Home no longer gives one.
func (g songFactsGrant) covers(other songFactsGrant) bool { return !other.names || g.names }

// factsGrammarFor is the grammar the reader gets under a grant: the format's own
// when names are allowed, the same grammar with every name key blanked when they
// are not, so a reader under a numbers-only consent has no way to read a name.
// Nil for a format that declares no grammar.
func factsGrammarFor(format string, names bool) *folderdigest.ProjectFacts {
	grammar := factsGrammar(format)
	if names {
		return grammar
	}
	return grammar.WithoutNames()
}

// factsWanted says a song's stored facts still need reading under a grant: it has
// none, or it was read under numbers only, the grant now covers names, and the
// format has names to give. A song that was not read, or whose format gives no
// names, is never read again for this (the same unchanged file gives the same).
func factsWanted(f *ObservedFacts, names bool, format string) bool {
	if f == nil {
		return true
	}
	return names && f.Names == nil && f.NotRead == "" && factsGrammar(format).HasNames()
}

// songFactsOutcome is what one pass did, for the log and tests. Counts only.
type songFactsOutcome struct {
	// Read counts files that gave facts; Skipped the rest of the targets (not
	// read, or not reached). Saved is how many records the write attached, and
	// Readable how many of those hold facts (the others record why a file was not
	// read).
	Targets, Opened, Read, Skipped int
	Bytes                          int64
	Stopped                        string // "", "time", "bytes", "cancelled", "authority"
	Saved, Readable                int
}

// SongFactsProgress is where one scan's pass has got to, for a caller that
// shows the reading as it happens ("Learning <song>… (2 of 7)"). Targets and
// Done count within this pass; a scan that lists a folder's songs in several
// scans runs one pass each, and the caller adds them up. Song is the song's
// folder name, never a path. The last report of a pass has Finished set, with
// Read the songs whose facts were saved and Stopped the reason it ended early
// ("time", "bytes", "cancelled" or "authority"; empty when it ran to the end).
type SongFactsProgress struct {
	Song     string
	Done     int
	Targets  int
	Read     int
	Skipped  int
	Finished bool
	Stopped  string
}

type songFactsProgressKey struct{}

// WithSongFactsProgress returns a context whose scans report their fact pass to
// report. The callback runs inside the pass, under the root's access gate: it
// must return at once and must not write to the library.
func WithSongFactsProgress(ctx context.Context, report func(SongFactsProgress)) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, songFactsProgressKey{}, report)
}

func songFactsProgressFrom(ctx context.Context) func(SongFactsProgress) {
	report, _ := ctx.Value(songFactsProgressKey{}).(func(SongFactsProgress))
	return report
}

// songFactsName is the name a progress report gives a song: its folder's name,
// or, for a song kept directly in the root, its file's name without extension.
func songFactsName(target songFactsTarget) string {
	name := path.Base(target.Relative)
	if target.Relative == "" || name == "." || name == "/" {
		name = strings.TrimSuffix(path.Base(target.File.File), path.Ext(target.File.File))
	}
	return name
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
	targets, grant, ok := r.songFactsTargets(scope, scan, observed)
	if !ok || len(targets) == 0 {
		return songFactsOutcome{}
	}
	started := time.Now()
	reads, outcome := r.songFactsPass(ctx, scope, root, targets, grant)
	if len(reads) > 0 {
		saved, readable, err := r.saveSongFacts(scope, scan, reads, grant)
		if err != nil {
			logger.Warn("Project library could not save song facts", logger.Fields{"home_id": scope.HomeID,
				"scan_id": scan.ID, "error": err.Error()})
		}
		outcome.Saved, outcome.Readable = saved, readable
	}
	if report := songFactsProgressFrom(ctx); report != nil {
		// Read counts the songs that gave facts; a song recorded as not read (or
		// not reached) is part of Skipped, so "i of N" and "Learned K" are honest.
		report(SongFactsProgress{Done: outcome.Targets, Targets: outcome.Targets, Read: outcome.Readable,
			Skipped: outcome.Targets - outcome.Readable, Finished: true, Stopped: outcome.Stopped})
	}
	logger.Info("Project library read song facts", logger.Fields{"home_id": scope.HomeID, "scan_id": scan.ID,
		"targets": outcome.Targets, "opened": outcome.Opened, "read": outcome.Read, "skipped": outcome.Skipped,
		"saved": outcome.Saved, "readable": outcome.Readable, "bytes": outcome.Bytes, "stopped": outcome.Stopped,
		"elapsed_ms": time.Since(started).Milliseconds()})
	return outcome
}

// songFactsTargets lists this scan's songs of a facts-declaring format whose
// facts are still wanted under the Home's grant (none stored, or stored under
// numbers only while the grant now covers names), most recently saved first. A
// Home without an active consent has none: the pass opens nothing for it. It
// also returns the grant the targets were chosen under.
func (r *Roots) songFactsTargets(scope Scope, scan Scan, observed Discovery) ([]songFactsTarget, songFactsGrant, bool) {
	doc, state, err := r.library.readSnapshot(scope)
	if err != nil {
		return nil, songFactsGrant{}, false
	}
	grant, ok := songFactsGrantOf(state)
	if !ok {
		return nil, songFactsGrant{}, false
	}
	var targets []songFactsTarget
	for _, candidate := range observed.Candidates {
		if candidate.DatedFile == nil || !factsFile(candidate.Format, candidate.DatedFile.File) {
			continue
		}
		observation := scanObservation(doc, scan, candidate.RelativeFolder, candidate.FileIdentity)
		if observation == nil || !factsWanted(observation.Facts, grant.names, candidate.Format) ||
			!factsCanAttach(*observation, *candidate.DatedFile) {
			continue
		}
		targets = append(targets, songFactsTarget{Relative: candidate.RelativeFolder,
			FolderIdentity: candidate.FileIdentity, Format: candidate.Format, File: *candidate.DatedFile})
	}
	sort.SliceStable(targets, func(i, j int) bool {
		return targets[i].File.ModifiedAt.After(targets[j].File.ModifiedAt)
	})
	return targets, grant, true
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
func (r *Roots) songFactsPass(ctx context.Context, scope Scope, root Root, targets []songFactsTarget, grant songFactsGrant) ([]songFactsRead, songFactsOutcome) {
	budget := r.songFactsBudgetOrDefault()
	outcome := songFactsOutcome{Targets: len(targets)}
	passCtx, cancel := context.WithTimeout(ctx, budget.Duration)
	defer cancel()
	var reads []songFactsRead
	for start := 0; start < len(targets) && outcome.Stopped == ""; start += songFactsBatch {
		end := min(start+songFactsBatch, len(targets))
		batch, stopped := r.songFactsBatch(passCtx, ctx, scope, root, targets[start:end], grant, budget, &outcome)
		reads = append(reads, batch...)
		outcome.Stopped = stopped
	}
	return reads, outcome
}

func (r *Roots) songFactsBatch(passCtx, ctx context.Context, scope Scope, root Root, targets []songFactsTarget,
	grant songFactsGrant, budget songFactsBudget, outcome *songFactsOutcome) ([]songFactsRead, string) {
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	if !r.songFactsAuthorityNoGate(scope, root, grant) {
		return nil, "authority"
	}
	var reads []songFactsRead
	stopped := ""
	report := songFactsProgressFrom(ctx)
	for _, target := range targets {
		if stopped = songFactsStop(passCtx, ctx); stopped != "" {
			break
		}
		if target.File.Size > DefaultSongFactsLimits.MaxBytes {
			// Over the cap: never opened, never facts, and remembered as too large
			// so the same unchanged file is not looked at again.
			outcome.Skipped++
			reads = append(reads, songFactsRead{songFactsTarget: target, NotRead: NotReadTooLarge})
			continue
		}
		if outcome.Bytes+target.File.Size > budget.Bytes {
			stopped = "bytes"
			break
		}
		if report != nil {
			report(SongFactsProgress{Song: songFactsName(target), Done: outcome.Read + outcome.Skipped,
				Targets: outcome.Targets, Read: outcome.Read, Skipped: outcome.Skipped})
		}
		read, ok := r.readOneSongFacts(root, target, grant, outcome)
		if !ok {
			outcome.Skipped++
			continue
		}
		reads = append(reads, read)
	}
	// Results from a root revoked, swapped or switched off during the batch
	// are dropped, as readDirectoryWithIdentity drops its rows.
	if !r.songFactsAuthorityNoGate(scope, root, grant) {
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
// re-checks it afterwards. An I/O failure or a changed file is skipped. The
// grammar the reader gets is chosen by the grant, so under a consent that does
// not cover names the reader cannot return one.
func (r *Roots) readOneSongFacts(root Root, target songFactsTarget, grant songFactsGrant, outcome *songFactsOutcome) (songFactsRead, bool) {
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
	facts, names, err := ReadSongDetails(file, factsGrammarFor(target.Format, grant.names), DefaultSongFactsLimits)
	if err != nil && !errors.Is(err, ErrSongFactsUnreadable) {
		return songFactsRead{}, false // an I/O failure says nothing about the file: tried again next scan
	}
	if !pinnedFileStillMatches(file, target.File) {
		return songFactsRead{}, false // re-saved while it was read
	}
	if err != nil {
		// The content gave nothing: over a cap, or not a project of its format, or
		// malformed. Remembered with the file's identity and the reason.
		reason := NotReadUnreadable
		if errors.Is(err, errSongFactsOverCap) {
			reason = NotReadTooLarge
		}
		outcome.Skipped++
		return songFactsRead{songFactsTarget: target, NotRead: reason}, true
	}
	outcome.Read++
	return songFactsRead{songFactsTarget: target, Facts: facts, Names: names}, true
}

// songFactsAuthorityNoGate is the pass's authority: the root is the one the scan
// used and still connected, and the Home's consent is still active and still
// covers what the pass began with (names, if it is reading names). The caller
// holds the root gate. A consent switched off, or narrowed, during a pass stops
// it here, before a batch is read and again before it is kept.
func (r *Roots) songFactsAuthorityNoGate(scope Scope, root Root, grant songFactsGrant) bool {
	live, state, err := r.verifyConnectedRootStateNoGate(scope, root.ID)
	if err != nil || live.Revision != root.Revision || live.Path != root.Path || live.FileIdentity != root.FileIdentity {
		return false
	}
	now, ok := songFactsGrantOf(state)
	return ok && now.covers(grant)
}

var errNoFactsChange = errors.New("project library song facts unchanged")

// saveSongFacts writes what the pass read. If the library is so full that the
// write is over its limit and some reads hold names, it writes once more with
// those names reduced to counts, so the numbers still land: a library never
// loses the listing, or a song's numbers, because of how many names it holds.
func (r *Roots) saveSongFacts(scope Scope, scan Scan, reads []songFactsRead, grant songFactsGrant) (saved, readable int, err error) {
	saved, readable, err = r.saveSongFactsWith(scope, scan, reads, grant, "")
	if errors.Is(err, ErrLimit) && readsHoldNames(reads) {
		logger.Warn("Project library is too full to keep song names; keeping the numbers", logger.Fields{
			"home_id": scope.HomeID, "scan_id": scan.ID})
		return r.saveSongFactsWith(scope, scan, namesAsCounts(reads), grant, ":counts")
	}
	return saved, readable, err
}

// saveSongFactsWith writes the facts in one fenced Home write keyed by the scan
// (and suffix), so a retry is a replay. A Home whose consent went off or no
// longer covers what the pass read, or whose observations moved on to a newer
// scan, takes none of them. The names of all songs together stay within the
// library's names budget: a read that would pass it is cut, and what was cut is
// counted as left out.
func (r *Roots) saveSongFactsWith(scope Scope, scan Scan, reads []songFactsRead, grant songFactsGrant, suffix string) (int, int, error) {
	payload, err := json.Marshal(reads)
	if err != nil {
		return 0, 0, ErrCorrupt
	}
	sum := sha256.Sum256(payload)
	op := operation{key: scan.ID + ":facts" + suffix, action: "scan_facts", digest: hex.EncodeToString(sum[:])}
	var saved, readable int
	for attempt := 0; attempt < 3; attempt++ {
		doc, err := r.library.Read(scope)
		if err != nil {
			return 0, 0, err
		}
		_, replay, err := r.library.mutateWithHomePolicy(scope, doc.Revision, op,
			func(state *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
				live, ok := songFactsGrantOf(state)
				return ok && live.covers(grant)
			}, func(current *Document) (string, error) {
				saved, readable = 0, 0
				// This closure runs again on a conflict: it cuts copies, never reads.
				room := max(0, maxLibraryNamesBytes-libraryNamesSize(current))
				for _, read := range reads {
					if read.Names != nil {
						read.Names = cutSongNames(read.Names, room)
					}
					if attachSongFacts(current, scan, read) {
						saved++
						if read.NotRead == "" {
							readable++
						}
						room = max(0, room-songNamesSize(read.Names))
					}
				}
				if saved == 0 {
					return "", errNoFactsChange
				}
				return scan.ID, nil
			})
		switch {
		case errors.Is(err, errNoFactsChange):
			return 0, 0, nil
		case errors.Is(err, ErrConflict) && !replay:
			continue // another write landed first; reapply on the fresh document
		case err != nil:
			return 0, 0, err
		case replay:
			return 0, 0, nil
		}
		return saved, readable, nil
	}
	return 0, 0, ErrConflict
}

// attachSongFacts sets one song's facts on the observation this scan recorded,
// only while they are still wanted (it has none, or only numbers and this read
// brings names) and it still names the file they were read from. A read that
// records why the file was not read goes on a song with no record, or on the
// empty record an older build stored for such a file.
func attachSongFacts(doc *Document, scan Scan, read songFactsRead) bool {
	for i := range doc.Entries {
		for j := range doc.Entries[i].Observations {
			o := &doc.Entries[i].Observations[j]
			if o.RootID != scan.RootID || o.RelativeFolder != read.Relative || o.FileIdentity != read.FolderIdentity {
				continue
			}
			if o.ScanID != scan.ID || !attachWanted(o.Facts, read, o.Format) || !factsCanAttach(*o, read.File) {
				return false
			}
			o.Facts = &ObservedFacts{SongFacts: read.Facts, Names: read.Names.clone(), NotRead: read.NotRead, ReadFrom: read.File}
			return true
		}
	}
	return false
}

// attachWanted says a read may replace what a song's observation holds. A read
// with facts follows factsWanted. A read that records a refusal replaces nothing
// but the empty record an older build kept for a file that gave no facts (which
// is what a names consent would otherwise read again on every scan).
func attachWanted(stored *ObservedFacts, read songFactsRead, format string) bool {
	if read.NotRead != "" {
		return stored == nil || (stored.NotRead == "" && stored.SongFacts.empty() && stored.Names == nil)
	}
	return factsWanted(stored, read.Names != nil, format)
}
