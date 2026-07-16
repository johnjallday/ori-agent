package personalhq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/session"
)

// JournalSnapshot is the bounded, grounded material an end-of-day journal is
// prefilled from (contract §7): completed work, notable email threads, and
// follow-up status changes for the local day. It excludes full transcripts and
// unrestricted email bodies — only short, already-sanitized summaries.
type JournalSnapshot struct {
	CompletedTasks  []string
	EmailSummaries  []string
	FollowUpChanges []string
	// Gaps names sources that could not be read, so the draft never implies a
	// quiet day when it was really an unavailable source.
	Gaps []string
}

// IsEmpty reports whether the snapshot has no grounded content.
func (s JournalSnapshot) IsEmpty() bool {
	return len(s.CompletedTasks) == 0 && len(s.EmailSummaries) == 0 && len(s.FollowUpChanges) == 0
}

// JournalSection is one titled group of grounded lines in the proposal.
type JournalSection struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

// JournalProposal is the editable end-of-day journal offered to the user. It has
// NO write side effect — nothing is persisted until the user explicitly saves
// (contract §7).
type JournalProposal struct {
	LocalDate string           `json:"local_date"`
	Sections  []JournalSection `json:"sections"`
	// Draft is the prefilled, editable journal text (markdown) the user reviews.
	Draft string `json:"draft"`
	// Gaps and Degraded surface incomplete grounding without blocking the entry.
	Gaps     []string `json:"gaps,omitempty"`
	Degraded bool     `json:"degraded"`
}

// SnapshotBuilder assembles the grounded journal snapshot for a user's local day.
type SnapshotBuilder interface {
	BuildJournalSnapshot(ctx context.Context, userID, localDate string) (JournalSnapshot, error)
}

// NoteWriter persists a saved journal as a workspace note. session.HybridStore
// satisfies it.
type NoteWriter interface {
	CreateNote(ctx context.Context, note *session.WorkspaceNote) error
}

// JournalService produces and saves end-of-day journals for the designated HQ.
type JournalService struct {
	service *Service
	builder SnapshotBuilder
	notes   NoteWriter
	now     func() time.Time
}

// NewJournalService constructs the journal service. builder may be nil (the
// proposal then degrades to an empty, still-editable draft).
func NewJournalService(service *Service, builder SnapshotBuilder, notes NoteWriter) *JournalService {
	return &JournalService{service: service, builder: builder, notes: notes, now: time.Now}
}

// Propose builds the editable end-of-day journal for localDate (a YYYY-MM-DD
// string in the user's timezone). It never writes anything.
func (j *JournalService) Propose(ctx context.Context, userID, localDate string) (*JournalProposal, error) {
	if j == nil {
		return nil, fmt.Errorf("journal service is not configured")
	}
	localDate = strings.TrimSpace(localDate)
	if localDate == "" {
		localDate = j.now().UTC().Format("2006-01-02")
	}

	var snap JournalSnapshot
	degraded := false
	if j.builder != nil {
		s, err := j.builder.BuildJournalSnapshot(ctx, userID, localDate)
		if err != nil {
			degraded = true
			snap.Gaps = append(snap.Gaps, "some activity could not be loaded")
		} else {
			snap = s
		}
	} else {
		degraded = true
	}

	sections := buildJournalSections(snap)
	return &JournalProposal{
		LocalDate: localDate,
		Sections:  sections,
		Draft:     BuildJournalDraft(localDate, snap),
		Gaps:      snap.Gaps,
		Degraded:  degraded,
	}, nil
}

// Save persists an explicitly-reviewed journal as a dated Personal HQ note. The
// default save path NEVER writes to MEMORY.md (contract §7) — memory promotion is
// a separate, explicit action.
func (j *JournalService) Save(ctx context.Context, userID, localDate, content string) (*session.WorkspaceNote, error) {
	if j == nil || j.notes == nil {
		return nil, fmt.Errorf("journal saving is not configured")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("journal content is empty")
	}
	status, err := j.service.Status(ctx, userID)
	if err != nil {
		return nil, err
	}
	if status == nil || !status.Valid {
		return nil, fmt.Errorf("no valid Personal HQ is designated")
	}
	localDate = strings.TrimSpace(localDate)
	if localDate == "" {
		localDate = j.now().UTC().Format("2006-01-02")
	}
	now := j.now().UTC()
	note := &session.WorkspaceNote{
		ID:          uuid.NewString(),
		WorkspaceID: status.WorkspaceID,
		Name:        "Journal — " + localDate,
		Content:     content,
		Tags:        []string{"journal"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := j.notes.CreateNote(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func buildJournalSections(snap JournalSnapshot) []JournalSection {
	var out []JournalSection
	if len(snap.CompletedTasks) > 0 {
		out = append(out, JournalSection{Title: "Completed", Items: snap.CompletedTasks})
	}
	if len(snap.FollowUpChanges) > 0 {
		out = append(out, JournalSection{Title: "Follow-ups", Items: snap.FollowUpChanges})
	}
	if len(snap.EmailSummaries) > 0 {
		out = append(out, JournalSection{Title: "Notable threads", Items: snap.EmailSummaries})
	}
	return out
}

// BuildJournalDraft renders a prefilled, editable markdown draft from the
// grounded snapshot. Pure and side-effect-free. An empty snapshot yields a
// gentle reflection prompt rather than fabricated accomplishments (contract §7).
func BuildJournalDraft(localDate string, snap JournalSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# End of day — %s\n\n", localDate)
	if snap.IsEmpty() {
		b.WriteString("What went well today, and what carries over to tomorrow?\n")
		return b.String()
	}
	for _, sec := range buildJournalSections(snap) {
		fmt.Fprintf(&b, "## %s\n", sec.Title)
		for _, item := range sec.Items {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Reflection\n")
	b.WriteString("What carries over to tomorrow?\n")
	return b.String()
}
