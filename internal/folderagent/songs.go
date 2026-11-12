package folderagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Answers to the offer to learn the songs.
const (
	SongsLearn     = "learn"
	SongsDismissed = "dismissed"
)

// How the first reading of the songs stands. Summarizing is the short stretch
// after the reading, while the agent writes what it learned.
const (
	LearningRunning     = "running"
	LearningSummarizing = "summarizing"
	LearningDone        = "done"
	LearningStopped     = "stopped"
)

// Who wrote the summary a reading ends with.
const (
	SummaryByAgent = "agent"
	SummaryByHost  = "host"
)

// learningFlushEvery is how often a running reading's progress is written to
// the record. The library's pass reports every song, but only from inside its
// own gate, so those reports just note the latest and this timer writes it.
const learningFlushEvery = 300 * time.Millisecond

// Learning is the first reading of the songs, started by "Learn them". Total is
// how many songs can be read this way, Done how many the reading has been
// through, Read how many it got numbers from, Song the one it is on (its name,
// never a path). It lives on the record; nothing here is a path or file text.
type Learning struct {
	Status     string    `json:"status"`
	Total      int       `json:"total"`
	Done       int       `json:"done"`
	Read       int       `json:"read"`
	Song       string    `json:"song,omitempty"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	// Summary is what the reading ends with. When SummaryBy is SummaryByAgent it
	// is the agent's own words, at most four sentences, written after the facts
	// and checked and bounded by the host; when SummaryByHost it is the host's
	// line of ranges ("6–24 tracks, 84–128 BPM") because there was no model or the
	// turn failed. Goal is the goal the agent wrote from the same facts (empty
	// when it wrote none that survived); the goal offer uses it before the first
	// look's.
	Summary   string `json:"summary,omitempty"`
	SummaryBy string `json:"summary_by,omitempty"`
	Goal      string `json:"goal,omitempty"`
}

// songsReads is what the offer says it will read, and what it will not touch.
// It is the one sentence that promises the limit, so it names exactly what the
// reader takes from a project file under the consent "Learn them" grants: the
// three numbers, and the names a person typed (tracks, markers and regions).
const songsReads = "I'll read tempo, length, track count, track names, markers and regions from each project file. Nothing is changed, and audio is not opened."

// Songs is the Home's song-details consent as the folder agent needs it: the
// offer to learn the songs is made only while the Home holds none.
type Songs interface {
	// Consent is the Home's song-details state (workspace.SongDetailsNone, On or
	// Off) and whether an active consent also reaches track names, markers and
	// regions. An error means the state could not be read.
	Consent(ctx context.Context, userID, workspaceID, homeID string) (state string, names bool, err error)
	// Grant records the consent for the owner's "Learn them", on the Home the
	// folder was set up in, covering the numbers and the names (the words of the
	// offer say both). It refuses (an error) when the folder is not in that Home
	// or the Home's switch was turned off; it never turns a switch back on.
	Grant(ctx context.Context, userID, workspaceID, homeID string) error
	// Learn reads the folder's songs under that consent: it lists the folder's
	// songs again so the library's own pass reads them, and returns how it
	// ended. report is called with the reading's progress as it goes; it never
	// blocks. Nothing is read unless the Home holds a consent.
	Learn(ctx context.Context, rec Record, homeID string, total int, report func(Learning)) Learning
	// Facts is this folder's songs with what was learned from each (the numbers,
	// and the track names, markers and regions under a consent that covers
	// them), for the folder's own agent. It is empty unless the Home's consent is
	// active. Only the folder's own agent is given it.
	Facts(ctx context.Context, rec Record, homeID string) ([]folderlook.Song, error)
}

// SongsOfferView is the Commander's offer to learn the songs, made once the
// Set it up run that placed the folder in a Home has finished and before the
// goal offer (Learn them, Not now). Nil when no offer is waiting. The words are
// the host's; nothing is read until the owner says yes.
type SongsOfferView struct {
	// Count is how many songs can be read this way, Noun the plural word for them.
	Count int    `json:"count"`
	Noun  string `json:"noun"`
	// Ask is the question; Reads says what is read and that nothing changes.
	Ask   string `json:"ask"`
	Reads string `json:"reads"`
	// NotRead names the songs that cannot be read this way, by format; empty
	// when every song can.
	NotRead string `json:"not_read,omitempty"`
}

// songsAnswerable says the offer can still be answered: nobody has answered it,
// or the owner said yes but nothing was granted and nothing is running (Ori
// stopped between the two). A "Not now", and a reading under way, are final.
func songsAnswerable(rec *Record) bool {
	switch rec.SongsAnswer {
	case "":
		return true
	case SongsLearn:
		return rec.Learning == nil || rec.Learning.Status != LearningRunning
	}
	return false
}

// learningRunning says a reading of the songs is under way: still reading, or
// writing up what it learned.
func learningRunning(rec *Record) bool {
	return rec != nil && rec.Learning != nil &&
		(rec.Learning.Status == LearningRunning || rec.Learning.Status == LearningSummarizing)
}

// songsHome is the Home a finished Set it up run placed the folder in, as the
// run's receipt names it; "" when the run placed the folder nowhere.
func songsHome(rec *Record) string {
	if rec == nil || rec.State != StateSetUp || rec.Setup == nil || rec.Setup.Status != personalassistant.FolderSetupDone ||
		rec.Setup.Receipt == nil || rec.Setup.Receipt.Home == nil {
		return ""
	}
	return strings.TrimSpace(rec.Setup.Receipt.Home.ID)
}

// songsOffer is the offer to learn the songs when one is due, nil otherwise.
//
// It is due when the folder was set up under a build that makes it, the run is
// done and put the folder in a Home, at least one song is in a format that can
// be read offline, the owner has not answered, and the Home holds no
// song-details consent at all. A Home whose switch is on already reads its
// songs (a newly added folder is learned by its normal scan); one whose switch
// is off was turned off by a person and is not asked again, because the Home's
// switch is the way back. A state that cannot be read makes no offer.
func (s *Service) songsOffer(ctx context.Context, rec *Record) *SongsOfferView {
	if s.deps.Songs == nil || rec == nil || !rec.OffersSongs || !songsAnswerable(rec) {
		return nil
	}
	homeID := songsHome(rec)
	if homeID == "" {
		return nil
	}
	readability := rec.Facts.Readability()
	if readability.Readable <= 0 {
		return nil
	}
	state, _, err := s.deps.Songs.Consent(ctx, rec.OwnerUserID, rec.WorkspaceID, homeID)
	if err != nil || state != workspace.SongDetailsNone {
		return nil
	}
	return &SongsOfferView{
		Count:   readability.Readable,
		Noun:    foldermap.ShapeNoun(readability.Shape, readability.Readable),
		Ask:     fmt.Sprintf("Want me to learn the %d %s?", readability.Readable, foldermap.ShapeNoun(readability.Shape, readability.Readable)),
		Reads:   songsReads,
		NotRead: notReadSentence(readability),
	}
}

// notReadSentence names the songs this reading cannot take, by format: "1
// <App> song can't be read this way." It is empty when every song can be read.
// A format the host has no name for is counted as "other".
func notReadSentence(r foldermap.Readability) string {
	var parts []string
	other := 0
	for _, u := range r.Unreadable {
		if u.Name == "" {
			other += u.Count
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s %s", u.Count, u.Name, foldermap.ShapeNoun(r.Shape, u.Count)))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("%d other %s", other, foldermap.ShapeNoun(r.Shape, other)))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0] + " can't be read this way."
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1] + " can't be read this way."
}

// AnswerSongs is the owner's answer to the offer to learn the songs. Not now
// records the answer and reads nothing. Learn them records the answer, grants
// the Home's consent, and reads the songs in the background; the card follows
// it through the record's Learning. The offer is answered once.
//
// The answer is recorded before the grant: two clicks cannot both pass the
// check, and a grant that fails (or a Home whose switch is off) puts the offer
// back exactly as it was.
func (s *Service) AnswerSongs(ctx context.Context, userID, workspaceID string, learn bool) (View, error) {
	current, err := s.Record(userID, workspaceID)
	if err != nil {
		return View{}, err
	}
	offer := s.songsOffer(ctx, current)
	if offer == nil {
		return View{}, ErrWrongState
	}
	if !learn {
		rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
			if !songsAnswerable(r) {
				return ErrWrongState
			}
			r.SongsAnswer = SongsDismissed
			return nil
		})
		if err != nil {
			return View{}, err
		}
		return s.viewOf(ctx, rec), nil
	}
	homeID := songsHome(current)
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		if !songsAnswerable(r) {
			return ErrWrongState
		}
		r.SongsAnswer = SongsLearn
		r.Learning = &Learning{Status: LearningRunning, Total: offer.Count, StartedAt: s.deps.Now().UTC()}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	if err := s.deps.Songs.Grant(ctx, userID, workspaceID, homeID); err != nil {
		_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
			r.SongsAnswer, r.Learning = current.SongsAnswer, current.Learning
			return nil
		})
		return View{}, err
	}
	s.wg.Add(1)
	// The reading outlives the request that asked for it: the request's values
	// travel with it, its cancellation does not.
	go s.runLearning(context.WithoutCancel(ctx), *rec, homeID, offer.Count)
	return s.viewOf(ctx, rec), nil
}

// runLearning reads the songs and keeps the record's Learning current. The
// library reports from inside its own gate, so a report only notes the latest
// progress; a timer writes it to the record, and the end is written last.
func (s *Service) runLearning(ctx context.Context, rec Record, homeID string, total int) {
	defer s.wg.Done()
	var mu sync.Mutex
	latest := Learning{Status: LearningRunning, Total: total, StartedAt: rec.Learning.StartedAt}
	report := func(l Learning) {
		mu.Lock()
		latest = l
		mu.Unlock()
	}
	flush := func() {
		mu.Lock()
		l := latest
		mu.Unlock()
		l.Status, l.Total, l.StartedAt = LearningRunning, total, rec.Learning.StartedAt
		_, _ = s.deps.Store.Update(rec.WorkspaceID, func(r *Record) error {
			if learningRunning(r) {
				r.Learning = &l
			}
			return nil
		})
	}
	stop, flushed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(flushed)
		tick := time.NewTicker(learningFlushEvery)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				flush()
			case <-stop:
				return
			}
		}
	}()
	final := s.deps.Songs.Learn(ctx, rec, homeID, total, report)
	close(stop)
	<-flushed
	if final.Status != LearningDone {
		final.Status = LearningStopped
	}
	final.Total, final.StartedAt = total, rec.Learning.StartedAt
	final.Done, final.Read = min(max(final.Done, 0), total), min(max(final.Read, 0), total)
	if final.Status == LearningDone && final.Read > 0 {
		// The reading is over and the facts are stored. The agent now writes what
		// it learned and a goal from it; the goal offer waits for that, so the
		// card says so while it does. A reading that stopped, or read nothing,
		// has nothing to summarise.
		pending := final
		pending.Status, pending.Song = LearningSummarizing, ""
		if s.updateLearning(rec.WorkspaceID, pending) {
			final.Summary, final.SummaryBy, final.Goal = s.summarize(ctx, rec, homeID)
		}
	}
	final.FinishedAt = s.deps.Now().UTC()
	s.updateLearning(rec.WorkspaceID, final)
}

// updateLearning writes a reading's state, only while the reading is still under
// way (a restart's sweep may have closed it). It says whether it wrote.
func (s *Service) updateLearning(workspaceID string, l Learning) bool {
	applied := false
	_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
		if learningRunning(r) {
			r.Learning = &l
			applied = true
		}
		return nil
	})
	return applied
}

// summarize is the second turn: once the songs are read, the folder's own agent
// is given this folder's facts (numbers, and track and marker names under a
// consent that covers them) and writes a short summary and a goal from them. It
// returns the summary, who wrote it, and the goal.
//
// The host's own line stands in whenever the agent cannot: no model chosen, a
// provider that is not available, a failed or empty reply, or a summary that did
// not survive the parser's bounds. The host line carries ranges only
// ("6–24 tracks, 84–128 BPM"). A goal that did not survive is empty, and the goal
// offer then falls back to the first look's. Nothing a name says is ever acted
// on: the turn has no tools and its reply is parsed, bounded and checked for a
// path before anything is kept.
func (s *Service) summarize(ctx context.Context, rec Record, homeID string) (text, by, goal string) {
	songs, err := s.deps.Songs.Facts(ctx, rec, homeID)
	if err != nil {
		logger.Warn("Folder agent could not read the songs it learned", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
		return "", "", ""
	}
	if len(songs) == 0 {
		return "", "", ""
	}
	hostText, hostBy := learnedRanges(songs), SummaryByHost
	if hostText == "" {
		hostBy = ""
	}
	resolved := s.resolveModel(ctx, &rec)
	if resolved.chat == nil {
		return hostText, hostBy, ""
	}
	reference, _ := folderlook.BuildSongsReference(folderlook.SongsReferenceInput{
		FolderName: rec.FolderName, Noun: foldermap.ShapeNoun(rec.Facts.Readability().Shape, 2), Songs: songs,
	})
	result := folderlook.RunSummaryTurn(ctx, folderlook.SummaryTurnInput{
		Chat: resolved.chat, Model: resolved.name, AgentName: rec.AgentName, Reference: reference,
	})
	if result.Err != nil || result.Reply == "" {
		fields := logger.Fields{"workspace_id": rec.WorkspaceID, "provider": resolved.provider, "model": resolved.name}
		if result.Err != nil {
			fields["error"] = result.Err.Error()
		}
		logger.Warn("Folder agent could not summarise what it learned", fields)
		return hostText, hostBy, ""
	}
	parsed := folderlook.ParseSummary(result.Reply, nestedFolderPaths(rec.Map))
	goal = parsed.Goal
	if goal != "" {
		// The same check the owner's own goal gets: one line, bounded, nothing
		// that looks like a secret.
		if normalized, err := personalassistant.NormalizeFolderBrief(goal); err == nil {
			goal = normalized
		} else {
			goal = ""
		}
	}
	if parsed.Text == "" {
		return hostText, hostBy, goal
	}
	return parsed.Text, SummaryByAgent, goal
}

// nestedFolderPaths are the map's relative folder paths that hold a slash: a
// summary quoting one names a path and is dropped.
func nestedFolderPaths(m *foldermap.Map) []string {
	if m == nil {
		return nil
	}
	var nested []string
	for _, f := range m.Folders {
		if strings.Contains(f.Path, "/") {
			nested = append(nested, f.Path)
		}
	}
	return nested
}

// learnedRanges is the host's own line about what was read: the range of track
// counts and of tempos across the songs that gave numbers, "6–24 tracks, 84–128
// BPM". Empty when no song gave either.
func learnedRanges(songs []folderlook.Song) string {
	return folderlook.SongRanges(songs)
}

// LearningView is the reading of the songs as the card and the Commander say it.
// Line is the one line to show; NotRead (set once it ended) names the songs that
// cannot be read this way. Both are the host's words.
type LearningView struct {
	Status  string `json:"status"`
	Line    string `json:"line"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	NotRead string `json:"not_read,omitempty"`
	// Summary is the agent's own words about what it learned (SummaryBy "agent"):
	// shown as the agent's, never as the host's. When the host wrote the summary
	// (no model, or the turn failed) it is part of Line instead and Summary is
	// empty, so it is said once.
	Summary   string `json:"summary,omitempty"`
	SummaryBy string `json:"summary_by,omitempty"`
}

// learningView words a record's reading, or nil when none was started.
//
//	running      "Learning <song>… (2 of 7)"
//	summarizing  "Putting together what I learned…"
//	done         "Learned 7 songs"  /  "Learned 5 of 7 songs"  /  "No song could be read",
//	             and, when the host wrote the summary, "Learned 7 songs: 6–24 tracks, 84–128 BPM"
//	stopped      "Stopped before it finished. Learned 3 of 7 songs"
func learningView(rec *Record) *LearningView {
	l := rec.Learning
	if l == nil {
		return nil
	}
	readability := rec.Facts.Readability()
	noun := func(n int) string { return foldermap.ShapeNoun(readability.Shape, n) }
	view := &LearningView{Status: l.Status, Done: l.Done, Total: l.Total}
	switch l.Status {
	case LearningRunning:
		position := min(l.Done+1, l.Total)
		if l.Song == "" {
			view.Line = fmt.Sprintf("Learning the %d %s…", l.Total, noun(l.Total))
		} else {
			view.Line = fmt.Sprintf("Learning %s… (%d of %d)", l.Song, position, l.Total)
		}
	case LearningSummarizing:
		view.Line = "Putting together what I learned…"
	case LearningDone, LearningStopped:
		learned := ""
		switch {
		case l.Read == 0 && l.Status == LearningDone:
			learned = "No song could be read"
		case l.Read == 0:
			learned = ""
		case l.Read == l.Total:
			learned = fmt.Sprintf("Learned %d %s", l.Read, noun(l.Read))
		default:
			learned = fmt.Sprintf("Learned %d of %d %s", l.Read, l.Total, noun(l.Total))
		}
		switch {
		case l.Status == LearningStopped && learned == "":
			view.Line = "Stopped before it finished"
		case l.Status == LearningStopped:
			view.Line = "Stopped before it finished. " + learned
		default:
			view.Line = learned
			switch {
			case l.Summary != "" && l.SummaryBy == SummaryByHost && learned != "":
				view.Line = learned + ": " + l.Summary
			case l.Summary != "" && l.SummaryBy == SummaryByAgent:
				view.Summary, view.SummaryBy = l.Summary, SummaryByAgent
			}
		}
		view.NotRead = notReadSentence(readability)
	}
	return view
}

// Where the goal the offer makes came from.
const (
	GoalBasisLearned = "learned"
	GoalBasisLook    = "look"
)

// offeredGoal is the goal the offer makes and where it came from: the one the
// agent wrote after learning the songs, when it wrote one that survived the
// host's checks, otherwise the first look's suggestion. Empty when neither exists.
func offeredGoal(rec *Record) (goal, basis string) {
	if rec == nil {
		return "", ""
	}
	if l := rec.Learning; l != nil && l.Status == LearningDone && strings.TrimSpace(l.Goal) != "" {
		return strings.TrimSpace(l.Goal), GoalBasisLearned
	}
	if rec.Sections != nil {
		if goal := strings.TrimSpace(rec.Sections.Goal); goal != "" {
			return goal, GoalBasisLook
		}
	}
	return "", ""
}
