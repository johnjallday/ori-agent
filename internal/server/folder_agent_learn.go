package server

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// folderAgentLearnLibrary is the Home library the reading of the songs needs:
// the listings, and the root they list.
type folderAgentLearnLibrary interface {
	folderAgentLibrary
	PortfolioLibraryState(ownerUserID, homeID, folder string) (sessionhttp.PortfolioLibraryState, error)
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
	token   func() string
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
		token: func() string { return strconv.FormatInt(time.Now().UnixNano(), 36) },
	}
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
	_, err = listFolderAgentSongs(ctx, library, rec, rec.OwnerUserID, homeID, state.RootID, key, "", "", func(string, string, string) {})
	if err != nil {
		logger.Warn("Folder agent could not list the songs to read them", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
	}
	return progress.final(err == nil)
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
}

func (p *learnProgress) note(sp projectlibrary.SongFactsProgress) {
	p.mu.Lock()
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
		Song: p.song,
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
	}
	return l
}
