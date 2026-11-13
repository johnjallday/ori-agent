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

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Song facts are saved after a consenting Home's scan is recorded. The host does
// not read project files itself: an installed app plugin's reader does (see
// project_facts_read.go and facts_read_run.go). This file holds what the
// plugin read turn shares: the songs whose facts are still wanted, the grant
// they are read under, and the fenced write that saves them. The write never
// changes the listing. A song that is not reached keeps no facts until a later
// scan.

// songFactsTarget is one song whose dated file the read may ask a plugin for.
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

// factsWanted says a song's stored facts still need reading under a grant: it has
// none, or it was read under numbers only and the grant now covers names. A song
// that was not read is never read again for this (the same unchanged file gives
// the same).
func factsWanted(f *ObservedFacts, names bool) bool {
	if f == nil {
		return true
	}
	return names && f.Names == nil && f.NotRead == ""
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
	// Reader is the agent reading the songs, when the Home's app assistant reads
	// them through its plugin; empty for the host's own pass.
	Reader string
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

// songFactsTargets lists this scan's songs whose facts are still wanted under
// the Home's grant (none stored, or stored under numbers only while the grant
// now covers names), most recently saved first. A
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
		if observation == nil || !factsWanted(observation.Facts, grant.names) ||
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

// songFactsAuthorityNoGate is the read's authority: the root is the one the scan
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
			if o.ScanID != scan.ID || !attachWanted(o.Facts, read) || !factsCanAttach(*o, read.File) {
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
func attachWanted(stored *ObservedFacts, read songFactsRead) bool {
	if read.NotRead != "" {
		return stored == nil || (stored.NotRead == "" && stored.empty() && stored.Names == nil)
	}
	return factsWanted(stored, read.Names != nil)
}
