package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"time"
)

// GoalInput and RecapInput contain user-authored production notes only. They
// are not tasks, chat sessions, file observations, or evidence of live DAW work.
type GoalInput struct {
	Goal        string `json:"goal"`
	Outcome     string `json:"desired_outcome,omitempty"`
	TimeMinutes int    `json:"time_minutes,omitempty"`
	PlannedDate string `json:"planned_date,omitempty"`
}

type RecapInput struct {
	Recap      string   `json:"recap"`
	Decisions  []string `json:"decisions,omitempty"`
	Blockers   []string `json:"blockers,omitempty"`
	ActualDate string   `json:"actual_date,omitempty"`
	Next       string   `json:"next_action,omitempty"`
	UpdateNext bool     `json:"update_project_next_action,omitempty"`
}

type SessionReview struct {
	Token     string        `json:"token"`
	Session   StudioSession `json:"session"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// SessionSummary omits potentially large decision/blocker lists; one record
// can be fetched separately by its exact Home-scoped ID when needed.
type SessionSummary struct {
	ID        string    `json:"id"`
	EntryID   string    `json:"entry_id"`
	Revision  int64     `json:"revision"`
	Goal      string    `json:"goal"`
	Outcome   string    `json:"desired_outcome,omitempty"`
	Recap     string    `json:"recap,omitempty"`
	Next      string    `json:"next_action,omitempty"`
	Author    string    `json:"author"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SessionPage struct {
	Revision   int64            `json:"revision"`
	Total      int              `json:"total"`
	Rows       []SessionSummary `json:"rows"`
	NextOffset int              `json:"next_offset,omitempty"`
}

type ResumeCard struct {
	EntryID           string         `json:"entry_id"`
	Name              string         `json:"name"`
	ProjectNextAction string         `json:"project_next_action,omitempty"`
	WorkspaceID       string         `json:"workspace_id,omitempty"` // Only a freshly verified reciprocal link.
	Session           SessionSummary `json:"session"`
}

type ResumeView struct {
	Revision int64        `json:"revision"`
	Cards    []ResumeCard `json:"cards"`
}

func sessionSummary(row StudioSession) SessionSummary {
	return SessionSummary{ID: row.ID, EntryID: row.EntryID, Revision: row.Revision,
		Goal: row.Goal, Outcome: row.Outcome, Recap: row.Recap, Next: row.Next,
		Author: row.Author, UpdatedAt: row.UpdatedAt}
}

// Resume is a read-only Home-wide view of at most three distinct catalog
// entries. It never traverses roots or joins project transcripts/Tickets.
func (s *Store) Resume(scope Scope) (ResumeView, error) {
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ResumeView{}, err
	}
	linked := make(map[string]bool, len(state.LinkedProjectIDs))
	for _, id := range state.LinkedProjectIDs {
		linked[id] = true
	}
	latest := make(map[string]StudioSession)
	for _, record := range doc.Sessions {
		if record.State != "accepted" {
			continue
		}
		if !validText(record.ID, 160) {
			return ResumeView{}, ErrCorrupt
		}
		previous, exists := latest[record.EntryID]
		if !exists || record.UpdatedAt.After(previous.UpdatedAt) ||
			record.UpdatedAt.Equal(previous.UpdatedAt) && record.ID < previous.ID {
			latest[record.EntryID] = record
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := latest[ids[i]], latest[ids[j]]
		if left.UpdatedAt.Equal(right.UpdatedAt) {
			return ids[i] < ids[j]
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
	view := ResumeView{Revision: doc.Revision, Cards: []ResumeCard{}}
	for _, id := range ids {
		if len(view.Cards) == 3 {
			break
		}
		entry := sessionEntry(doc, id)
		if entry == nil || !validText(id, 160) {
			return ResumeView{}, ErrCorrupt
		}
		name := entry.Fields.DisplayName
		if name == "" {
			for _, observed := range entry.Observations {
				if observed.RelativeFolder != "" {
					name = filepath.Base(observed.RelativeFolder)
					break
				}
			}
		}
		if name == "" {
			name = "Untitled project"
		}
		card := ResumeCard{EntryID: id, Name: name,
			ProjectNextAction: entry.Fields.NextAction, Session: sessionSummary(latest[id])}
		if row := s.projectSearchRow(scope, *entry, nil, linked, nil); row.Connection == "connected" {
			card.WorkspaceID = entry.Link.WorkspaceID
		}
		view.Cards = append(view.Cards, card)
	}
	return view, nil
}

func validSessionAuthor(author string) bool { return author != "" && validText(author, 160) }

func (input GoalInput) valid() bool {
	return input.Goal != "" && validText(input.Goal, 500) && validText(input.Outcome, 500) &&
		input.TimeMinutes >= 0 && input.TimeMinutes <= 480 && validDate(input.PlannedDate)
}

func (input RecapInput) valid() bool {
	if input.Recap == "" || !validText(input.Recap, 2000) || !validText(input.Next, 240) ||
		!validDate(input.ActualDate) || len(input.Decisions) > 16 || len(input.Blockers) > 16 ||
		(input.UpdateNext && input.Next == "") {
		return false
	}
	for _, list := range [][]string{input.Decisions, input.Blockers} {
		for _, value := range list {
			if value == "" || !validText(value, 240) {
				return false
			}
		}
	}
	return true
}

func sessionDigest(scope Scope, action, entryID, sessionID string, entryRevision, sessionRevision, fieldsRevision int64, input any, author string) (string, error) {
	encoded, err := json.Marshal(struct {
		Scope           Scope  `json:"scope"`
		Action          string `json:"action"`
		EntryID         string `json:"entry_id"`
		SessionID       string `json:"session_id"`
		EntryRevision   int64  `json:"entry_revision"`
		SessionRevision int64  `json:"session_revision"`
		FieldsRevision  int64  `json:"fields_revision"`
		Input           any    `json:"input"`
		Author          string `json:"author"`
	}{scope, action, entryID, sessionID, entryRevision, sessionRevision, fieldsRevision, input, author})
	if err != nil {
		return "", ErrCorrupt
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func sessionEntry(doc Document, id string) *Entry {
	for i := range doc.Entries {
		if doc.Entries[i].ID == id {
			return &doc.Entries[i]
		}
	}
	return nil
}

// GetSession returns one accepted user record, including its bounded decisions
// and blockers, only under the exact Home and catalog entry. It does not read
// a linked child's transcript, filesystem or task state.
func (s *Store) GetSession(scope Scope, entryID, sessionID string) (StudioSession, error) {
	if entryID == "" || sessionID == "" || !validText(entryID, 160) || !validText(sessionID, 160) {
		return StudioSession{}, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return StudioSession{}, err
	}
	if sessionEntry(doc, entryID) == nil {
		return StudioSession{}, ErrConflict
	}
	for _, record := range doc.Sessions {
		if record.ID == sessionID && record.EntryID == entryID && record.State == "accepted" {
			return record, nil
		}
	}
	return StudioSession{}, ErrConflict
}

// ListSessions is an inert, revision-bound page of one Home's own records. A
// catalog-only or revoked-source entry remains readable without a root grant.
func (s *Store) ListSessions(scope Scope, entryID string, revision int64, offset int) (SessionPage, error) {
	if entryID == "" || !validText(entryID, 160) || offset < 0 || offset > maxSessions || revision < 0 {
		return SessionPage{}, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return SessionPage{}, err
	}
	if sessionEntry(doc, entryID) == nil || (revision != 0 && revision != doc.Revision) {
		return SessionPage{}, ErrConflict
	}
	rows := make([]StudioSession, 0, 20)
	for _, record := range doc.Sessions {
		if record.EntryID == entryID && record.State == "accepted" {
			rows = append(rows, record)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})
	page := SessionPage{Revision: doc.Revision, Total: len(rows), Rows: []SessionSummary{}}
	if offset >= len(rows) {
		return page, nil
	}
	end := offset + 5 // Worst-case escaped text still fits the 128-KiB HTTP page budget.
	if end > len(rows) {
		end = len(rows)
	}
	for _, row := range rows[offset:end] {
		if !validText(row.ID, 160) {
			return SessionPage{}, ErrCorrupt
		}
		page.Rows = append(page.Rows, sessionSummary(row))
	}
	if end < len(rows) {
		page.NextOffset = end
	}
	return page, nil
}

// ReviewGoal saves only a short-lived review receipt, not a draft session or
// a project task. A catalog entry needs no project link to hold a session goal.
func (s *Store) ReviewGoal(scope Scope, entryID string, entryRevision int64, input GoalInput, author string) (SessionReview, error) {
	if s == nil || s.providerEvidence == nil {
		return SessionReview{}, ErrUnavailable
	}
	if entryID == "" || entryRevision < 1 || !input.valid() || !validSessionAuthor(author) {
		return SessionReview{}, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return SessionReview{}, err
	}
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Revision != entryRevision {
		return SessionReview{}, ErrConflict
	}
	now := s.now().UTC()
	planned := StudioSession{ID: newID(), EntryID: entryID, Revision: 1,
		Goal: input.Goal, Outcome: input.Outcome, TimeMinutes: input.TimeMinutes, PlannedDate: input.PlannedDate,
		Author: author, Source: "reviewed_user", State: "reviewed", CreatedAt: now, UpdatedAt: now}
	digest, err := sessionDigest(scope, "create_session_goal", entryID, planned.ID, entryRevision, 0, 0, input, author)
	if err != nil {
		return SessionReview{}, err
	}
	review := ReviewReceipt{Token: newID(), Action: "create_session_goal", TargetID: planned.ID,
		Digest: digest, Revision: doc.Revision + 1, FieldsRevision: entryRevision,
		ExpiresAt: now.Add(10 * time.Minute)}
	_, _, err = s.mutate(scope, doc.Revision,
		operation{key: review.Token, action: "review_session_goal", digest: digest}, func(current *Document) (string, error) {
			if len(current.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			current.Reviews = append(current.Reviews, review)
			return review.Token, nil
		})
	if err != nil {
		return SessionReview{}, err
	}
	return SessionReview{Token: review.Token, Session: planned, ExpiresAt: review.ExpiresAt}, nil
}

func (s *Store) CommitGoal(scope Scope, entryID, token, key string, entryRevision int64, input GoalInput, author string) (StudioSession, bool, error) {
	if s == nil || s.providerEvidence == nil {
		return StudioSession{}, false, ErrUnavailable
	}
	if token == "" || key == "" || !validText(key, 160) ||
		entryRevision < 1 || !input.valid() || !validSessionAuthor(author) {
		return StudioSession{}, false, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return StudioSession{}, false, err
	}
	var review *ReviewReceipt
	for i := range doc.Reviews {
		if doc.Reviews[i].Token == token && doc.Reviews[i].Action == "create_session_goal" &&
			doc.Reviews[i].FieldsRevision == entryRevision {
			review = &doc.Reviews[i]
			break
		}
	}
	if review == nil {
		return StudioSession{}, false, ErrConflict
	}
	digest, err := sessionDigest(scope, "create_session_goal", entryID, review.TargetID, entryRevision, 0, 0, input, author)
	if err != nil || digest != review.Digest {
		return StudioSession{}, false, ErrConflict
	}
	for _, prior := range doc.Operations {
		if prior.Key != key {
			continue
		}
		if prior.Action != "create_session_goal" || prior.Digest != digest || prior.ConsequenceID != review.TargetID {
			return StudioSession{}, false, ErrConflict
		}
		for _, session := range doc.Sessions {
			if session.ID == review.TargetID && session.EntryID == entryID {
				return session, true, nil
			}
		}
		return StudioSession{}, false, ErrCorrupt
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now().UTC()) || review.Revision != doc.Revision {
		return StudioSession{}, false, ErrConflict
	}
	var committed StudioSession
	_, replay, err := s.mutate(scope, doc.Revision,
		operation{key: key, action: "create_session_goal", digest: digest}, func(current *Document) (string, error) {
			if len(current.Sessions) >= maxSessions {
				return "", ErrLimit
			}
			entry := sessionEntry(*current, entryID)
			if entry == nil || entry.Revision != entryRevision {
				return "", ErrConflict
			}
			for i := range current.Reviews {
				receipt := &current.Reviews[i]
				if receipt.Token != token || receipt.Action != "create_session_goal" || receipt.Digest != digest ||
					receipt.ConsumedAt != nil || receipt.Revision != current.Revision || !receipt.ExpiresAt.After(s.now().UTC()) {
					continue
				}
				now := s.now().UTC()
				committed = StudioSession{ID: receipt.TargetID, EntryID: entryID, Revision: 1,
					Goal: input.Goal, Outcome: input.Outcome, TimeMinutes: input.TimeMinutes, PlannedDate: input.PlannedDate,
					Author: author, Source: "reviewed_user", State: "accepted", CreatedAt: now, AcceptedAt: &now, UpdatedAt: now}
				current.Sessions = append(current.Sessions, committed)
				receipt.ConsumedAt = &now
				return committed.ID, nil
			}
			return "", ErrConflict
		})
	if err != nil {
		return StudioSession{}, false, err
	}
	return committed, replay, nil
}

// ReviewRecap previews both consequences: the accepted session recap and, only
// if requested, an exact next-action patch to the same Home document.
func (s *Store) ReviewRecap(scope Scope, sessionID string, sessionRevision, fieldsRevision int64, input RecapInput, author string) (SessionReview, error) {
	if s == nil || s.providerEvidence == nil {
		return SessionReview{}, ErrUnavailable
	}
	if sessionID == "" || sessionRevision < 1 || fieldsRevision < 0 || !input.valid() || !validSessionAuthor(author) {
		return SessionReview{}, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return SessionReview{}, err
	}
	var previous StudioSession
	found := false
	for _, record := range doc.Sessions {
		if record.ID == sessionID && record.State == "accepted" {
			previous, found = record, true
			break
		}
	}
	entry := sessionEntry(doc, previous.EntryID)
	if !found || previous.Revision != sessionRevision || entry == nil || entry.Fields.Revision != fieldsRevision {
		return SessionReview{}, ErrConflict
	}
	now := s.now().UTC()
	planned := previous
	planned.Recap, planned.Decisions, planned.Blockers, planned.ActualDate, planned.Next =
		input.Recap, append([]string(nil), input.Decisions...), append([]string(nil), input.Blockers...), input.ActualDate, input.Next
	planned.Revision++
	planned.State, planned.Author, planned.UpdatedAt = "reviewed", author, now
	digest, err := sessionDigest(scope, "accept_session_recap", previous.EntryID, sessionID,
		entry.Revision, sessionRevision, fieldsRevision, input, author)
	if err != nil {
		return SessionReview{}, err
	}
	review := ReviewReceipt{Token: newID(), Action: "accept_session_recap", TargetID: sessionID,
		Digest: digest, Revision: doc.Revision + 1, FieldsRevision: fieldsRevision,
		EntryRevision: entry.Revision, ExpiresAt: now.Add(10 * time.Minute)}
	_, _, err = s.mutate(scope, doc.Revision,
		operation{key: review.Token, action: "review_session_recap", digest: digest}, func(current *Document) (string, error) {
			if len(current.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			current.Reviews = append(current.Reviews, review)
			return review.Token, nil
		})
	if err != nil {
		return SessionReview{}, err
	}
	return SessionReview{Token: review.Token, Session: planned, ExpiresAt: review.ExpiresAt}, nil
}

// CommitRecap updates the session and optionally the user-owned project next
// action in one Home CAS. A concurrent note edit makes both writes fail.
func (s *Store) CommitRecap(scope Scope, sessionID, token, key string, sessionRevision, fieldsRevision int64,
	input RecapInput, author string) (StudioSession, bool, error) {
	if s == nil || s.providerEvidence == nil {
		return StudioSession{}, false, ErrUnavailable
	}
	if sessionID == "" || token == "" || key == "" || !validText(key, 160) ||
		sessionRevision < 1 || fieldsRevision < 0 || !input.valid() || !validSessionAuthor(author) {
		return StudioSession{}, false, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return StudioSession{}, false, err
	}
	var previous StudioSession
	found := false
	for _, record := range doc.Sessions {
		if record.ID == sessionID {
			previous, found = record, true
			break
		}
	}
	if !found {
		return StudioSession{}, false, ErrConflict
	}
	entry := sessionEntry(doc, previous.EntryID)
	if entry == nil {
		return StudioSession{}, false, ErrCorrupt
	}
	var review *ReviewReceipt
	for i := range doc.Reviews {
		if doc.Reviews[i].Token == token && doc.Reviews[i].Action == "accept_session_recap" &&
			doc.Reviews[i].TargetID == sessionID && doc.Reviews[i].FieldsRevision == fieldsRevision {
			review = &doc.Reviews[i]
			break
		}
	}
	if review == nil {
		return StudioSession{}, false, ErrConflict
	}
	digest, err := sessionDigest(scope, "accept_session_recap", previous.EntryID, sessionID,
		review.EntryRevision, sessionRevision, fieldsRevision, input, author)
	if err != nil || digest != review.Digest {
		return StudioSession{}, false, ErrConflict
	}
	for _, prior := range doc.Operations {
		if prior.Key != key {
			continue
		}
		if prior.Action != "accept_session_recap" || prior.Digest != digest || prior.ConsequenceID != sessionID {
			return StudioSession{}, false, ErrConflict
		}
		return previous, true, nil
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now().UTC()) || review.Revision != doc.Revision {
		return StudioSession{}, false, ErrConflict
	}
	var committed StudioSession
	_, replay, err := s.mutate(scope, doc.Revision,
		operation{key: key, action: "accept_session_recap", digest: digest}, func(current *Document) (string, error) {
			for i := range current.Reviews {
				receipt := &current.Reviews[i]
				if receipt.Token != token || receipt.Action != "accept_session_recap" || receipt.Digest != digest ||
					receipt.ConsumedAt != nil || receipt.Revision != current.Revision || !receipt.ExpiresAt.After(s.now().UTC()) {
					continue
				}
				for j := range current.Sessions {
					session := &current.Sessions[j]
					if session.ID != sessionID || session.State != "accepted" || session.Revision != sessionRevision {
						continue
					}
					entry := sessionEntry(*current, session.EntryID)
					if entry == nil || entry.Fields.Revision != fieldsRevision || entry.Revision != review.EntryRevision {
						return "", ErrConflict
					}
					now := s.now().UTC()
					if input.UpdateNext {
						patched, patchErr := (FieldsPatch{NextAction: &input.Next}).apply(entry.Fields, author, now)
						if patchErr != nil {
							return "", patchErr
						}
						entry.Fields, entry.Revision = patched, entry.Revision+1
					}
					session.Recap, session.Decisions, session.Blockers, session.ActualDate, session.Next =
						input.Recap, append([]string(nil), input.Decisions...), append([]string(nil), input.Blockers...), input.ActualDate, input.Next
					session.Author, session.Revision, session.UpdatedAt, session.AcceptedAt = author, session.Revision+1, now, &now
					committed = *session
					receipt.ConsumedAt = &now
					return sessionID, nil
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		return StudioSession{}, false, err
	}
	return committed, replay, nil
}
