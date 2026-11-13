package server

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// folderAgentLearnLibrary is the Home library the reading of the songs needs:
// the listings, and the root they list.
type folderAgentLearnLibrary interface {
	folderAgentLibrary
	PortfolioLibraryState(ownerUserID, homeID, folder string) (sessionhttp.PortfolioLibraryState, error)
	FolderAgentSongs(ownerUserID, homeID, rootID string) ([]projectlibrary.LearnedSong, error)
}

// folderAgentSongs is the folder agent's side of the Home's song-details
// consent and of reading the songs under it (folderagent.Songs).
//
// The consent is the Home's, and the library's fact pass is the only thing that
// opens a project file: it opens none without an active consent, at the start,
// between batches and when it saves. This type grants the consent for the
// owner's "Learn them" and lists the folder's songs again so that pass reads
// them; it never reads a file itself.
type folderAgentSongs struct {
	consents *workspace.SongDetailsConsents
	store    interface {
		Get(id string) (*workspace.Workspace, error)
	}
	anchor func(workspaceID string) (string, error)
	// library is resolved when used: the session handler may not exist yet when
	// the folder agent is wired.
	library func() folderAgentLearnLibrary
	// runner is the Home assistant's read turn, resolved when used.
	runner func() *projectlibrary.FactsReadRunner
	// installed lists the installed plugins, for naming the one that reads.
	installed func() ([]plugin.InstalledPlugin, error)
	token     func() string
}

func newFolderAgentSongs(b *ServerBuilder) folderAgentSongs {
	return folderAgentSongs{
		consents: workspace.NewSongDetailsConsents(b.workspaceStore),
		store:    b.workspaceStore,
		anchor:   folderAgentWorkspaces{builder: b}.AnchorRoot,
		library: func() folderAgentLearnLibrary {
			if b.sessionHandler == nil {
				return nil
			}
			return b.sessionHandler
		},
		runner: func() *projectlibrary.FactsReadRunner {
			if b.sessionHandler == nil {
				return nil
			}
			return b.sessionHandler.FactsReadRunner()
		},
		installed: func() ([]plugin.InstalledPlugin, error) {
			if b.pluginHandler == nil {
				return nil, nil
			}
			return b.pluginHandler.Manager().List()
		},
		token: func() string { return strconv.FormatInt(time.Now().UnixNano(), 36) },
	}
}

// ReaderName is the app plugin that reads the songs, as the reviewed list names
// it, for the offer's words; "" when no installed plugin reads them.
func (f folderAgentSongs) ReaderName() string {
	if f.installed == nil {
		return ""
	}
	installed, err := f.installed()
	if err != nil {
		return ""
	}
	return projectlibrary.ReaderDisplayName(installed)
}

// Consent is the Home's song-details state and whether it reaches names.
func (f folderAgentSongs) Consent(_ context.Context, _, _, homeID string) (string, bool, error) {
	return f.consents.ReadScope(homeID)
}

// Grant records the consent for "Learn them", at names scope: the offer's words
// say tempo, length, track count, track names, markers and regions, so the
// consent covers all of them (a Home that already holds a numbers consent is
// widened, as a new and broader grant). It refuses when the folder's workspace is
// not inside that Home, and when the Home's switch is off: only the Home's own
// switch turns reading back on.
func (f folderAgentSongs) Grant(_ context.Context, _, workspaceID, homeID string) error {
	if f.store == nil {
		return folderagent.ErrUnavailable
	}
	ws, err := f.store.Get(workspaceID)
	if err != nil || ws == nil || homeID == "" || ws.ParentID != homeID {
		return folderagent.ErrWrongState
	}
	err = f.consents.GrantScope(homeID, workspace.SongDetailsConsentLearnSongs, "folder-agent:"+workspaceID, workspace.SongDetailsScopeNames)
	if errors.Is(err, workspace.ErrSongDetailsConsentRevoked) {
		return folderagent.ErrWrongState
	}
	return err
}

// Learn lists the folder's songs again under a fresh key, so each scan's fact
// pass reads the songs the Home now has a consent for, and adds up the passes'
// progress. Done is clamped to total: a song that is in two listings is counted
// once. A listing that cannot be made, or a pass that ended early, leaves the
// reading "stopped"; what was read stays read.
func (f folderAgentSongs) Learn(ctx context.Context, rec folderagent.Record, homeID string, total int, report func(folderagent.Learning)) folderagent.Learning {
	progress := &learnProgress{total: total, report: report}
	library := f.library()
	if library == nil || f.anchor == nil {
		return progress.final(false)
	}
	// The pass would open nothing without a consent anyway; not listing at all
	// keeps this from looking like a reading that happened.
	if state, _, err := f.consents.ReadScope(homeID); err != nil || state != workspace.SongDetailsOn {
		return progress.final(false)
	}
	folder, err := f.anchor(rec.WorkspaceID)
	if err != nil {
		return progress.final(false)
	}
	state, err := library.PortfolioLibraryState(rec.OwnerUserID, homeID, folder)
	if err != nil || state.RootID == "" {
		return progress.final(false)
	}
	key := "folder-agent-" + rec.WorkspaceID + "-learn-" + f.token()
	ctx = projectlibrary.WithSongFactsProgress(ctx, progress.note)
	// The songs an app plugin reads are gathered across the listing's scans and
	// read in one turn by the Home's assistant, so one press makes one turn. The
	// scans' events are held until that read ends.
	ctx, collector := projectlibrary.WithDeferredFactsRead(ctx)
	listing, err := listFolderAgentSongs(ctx, library, rec, rec.OwnerUserID, homeID, state.RootID, key, "", "", func(string, string, string) {})
	if err != nil {
		logger.Warn("Folder agent could not list the songs to read them", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
	}
	// Whatever was gathered is read (or stopped with its reason) and its held
	// events published, even when the listing ended early.
	if slices := collector.Slices(); len(slices) > 0 && f.runner != nil {
		if runner := f.runner(); runner != nil {
			progress.readEnded(runner.Read(ctx, slices, projectlibrary.FactsReadByLearn))
		}
	}
	// A folder of songs the listing never reached is a reading that did not
	// happen, not one that found nothing to read: it ends "stopped", and what was
	// read stays read.
	if err == nil && listing.unreached > 0 {
		logger.Warn("Folder agent could not reach every folder of songs to read them", logger.Fields{"workspace_id": rec.WorkspaceID, "folders": listing.unreached})
	}
	return progress.final(err == nil && listing.unreached == 0)
}

// Facts is this folder's songs with what was learned from each, for the folder's
// own agent: the numbers, and the track names, markers and regions under a
// consent that covers them. It gives nothing unless the Home's consent is on, and
// only the folder agent's own turns call it (no route, no tool of the Manager's).
func (f folderAgentSongs) Facts(_ context.Context, rec folderagent.Record, homeID string) ([]folderlook.Song, error) {
	library := f.library()
	if library == nil || f.anchor == nil {
		return nil, folderagent.ErrUnavailable
	}
	if state, _, err := f.consents.ReadScope(homeID); err != nil || state != workspace.SongDetailsOn {
		return nil, nil
	}
	folder, err := f.anchor(rec.WorkspaceID)
	if err != nil {
		return nil, err
	}
	state, err := library.PortfolioLibraryState(rec.OwnerUserID, homeID, folder)
	if err != nil {
		return nil, err
	}
	if state.RootID == "" {
		return nil, errors.New("the folder is not a root of the library")
	}
	songs, err := library.FolderAgentSongs(rec.OwnerUserID, homeID, state.RootID)
	if err != nil {
		return nil, err
	}
	return learnedSongs(songs), nil
}

// learnedSongs turns the library's songs into what a model turn is given.
func learnedSongs(in []projectlibrary.LearnedSong) []folderlook.Song {
	out := make([]folderlook.Song, 0, len(in))
	for _, song := range in {
		turn := folderlook.Song{Name: song.Name, NotRead: song.NotRead}
		if song.Facts != nil {
			turn.TempoBPM, turn.TempoVaries = song.Facts.TempoBPM, song.Facts.TempoVaries
			turn.LengthSeconds, turn.TrackCount = song.Facts.LengthSeconds, song.Facts.TrackCount
		}
		if song.Names != nil {
			turn.Tracks, turn.TracksLeftOut = append([]string(nil), song.Names.Tracks...), song.Names.TracksLeftOut
			turn.MarkersLeftOut = song.Names.MarkersLeftOut
			for _, m := range song.Names.Markers {
				turn.Markers = append(turn.Markers, folderlook.SongMarker{Name: m.Name, At: m.At, End: m.End})
			}
		}
		out = append(out, turn)
	}
	return out
}

// learnProgress adds up the fact passes of one reading. Each scan runs its own
// pass and reports within it; this keeps the running totals across them. It is
// called from inside the library's pass, so it only notes and reports (the
// report itself never blocks).
type learnProgress struct {
	mu     sync.Mutex
	total  int
	report func(folderagent.Learning)
	// doneBefore and readBefore are the finished passes' totals.
	doneBefore, readBefore int
	current                projectlibrary.SongFactsProgress
	song                   string
	stopped                bool
	// reader is the app assistant reading the songs; reason why its read
	// stopped (a folderagent.LearningReason* code), "" when it did not.
	reader, reason string
}

// readEnded takes the outcome of the Home assistant's read turn. Its reason
// codes are the library's, which the folder agent names the same way.
func (p *learnProgress) readEnded(run projectlibrary.FactsReadRun) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if run.AgentName != "" {
		p.reader = run.AgentName
	}
	if run.Status == "stopped" {
		p.stopped = true
		p.reason = run.Reason
	}
}

func (p *learnProgress) note(sp projectlibrary.SongFactsProgress) {
	p.mu.Lock()
	if sp.Reader != "" {
		p.reader = sp.Reader
	}
	if sp.Finished {
		p.doneBefore += sp.Targets
		p.readBefore += sp.Read
		p.current = projectlibrary.SongFactsProgress{}
		p.stopped = p.stopped || sp.Stopped != ""
	} else {
		p.current = sp
		p.song = sp.Song
	}
	snapshot := p.snapshotLocked(folderagent.LearningRunning)
	p.mu.Unlock()
	if p.report != nil {
		p.report(snapshot)
	}
}

func (p *learnProgress) snapshotLocked(status string) folderagent.Learning {
	return folderagent.Learning{
		Status: status, Total: p.total,
		Done: min(p.total, p.doneBefore+p.current.Done),
		Read: min(p.total, p.readBefore+p.current.Read),
		Song: p.song, Reader: p.reader,
	}
}

// final is how the reading ended: done when every listing was made and no pass
// stopped early, stopped otherwise.
func (p *learnProgress) final(listed bool) folderagent.Learning {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := folderagent.LearningDone
	if !listed || p.stopped {
		status = folderagent.LearningStopped
	}
	l := p.snapshotLocked(status)
	l.Song = ""
	if status == folderagent.LearningDone {
		l.Done = p.total
	} else {
		l.Reason = p.reason
	}
	return l
}
