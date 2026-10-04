package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

// A draft saved from a hired-assistant conversation is one ordinary Backlog
// Ticket. Nothing about it is stored anywhere else: the Ticket's own source key
// records which conversation message it came from and which reviewed save
// produced it, and that key is the durable evidence a retry is checked against.
//
//	assistant-draft:<conversation id>:<message id>:<operation id>:<payload digest>
//
// The source key is immutable provenance (TicketService.Update cannot change
// it), so later edits to the Ticket never disturb the link or the evidence.

const (
	assistantDraftSourcePrefix = "assistant-draft"
	assistantDraftDigestLength = 32
	assistantDraftTokenMax     = 64
	assistantDraftTitleChars   = 80
)

// ErrAssistantDraftConflict means a save's operation ID was already used for
// different content, a different target, or a different source message. The
// earlier save stands; the new payload is refused.
var ErrAssistantDraftConflict = errors.New("this save was already used for different content")

// AssistantDraftKey is the parsed source key of a saved draft.
type AssistantDraftKey struct {
	ConversationID string
	MessageID      string
	OperationID    string
	Digest         string
}

// AssistantDraftInput is one reviewed save. Title and Body are exactly what the
// user approved; WorkspaceID is the server-resolved Personal HQ.
type AssistantDraftInput struct {
	WorkspaceID    string
	ConversationID string
	MessageID      string
	OperationID    string
	Title          string
	Body           string
	// ActorID is the user who approved the save, for the Ticket's history.
	ActorID string
}

// AssistantDraftReceipt is the canonical result of a save.
type AssistantDraftReceipt struct {
	Ticket Ticket
	// Created is false when an earlier attempt of the same save already
	// created the Ticket; that Ticket is returned as it is now.
	Created bool
	// ChangedSince reports that the replayed Ticket no longer matches what was
	// saved: it was edited, moved out of Backlog, or both.
	ChangedSince bool
}

// AssistantDraftService saves reviewed drafts through the canonical Ticket
// service. It holds no state of its own.
type AssistantDraftService struct {
	backlog *BacklogService
}

// NewAssistantDraftService builds the service over the Backlog adapter, so a
// saved draft gets the same events and BACKLOG.md render as any other capture.
func NewAssistantDraftService(backlog *BacklogService) *AssistantDraftService {
	return &AssistantDraftService{backlog: backlog}
}

// isAssistantDraftToken reports whether an ID is safe to embed in a source key.
func isAssistantDraftToken(value string) bool {
	if value == "" || len(value) > assistantDraftTokenMax {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// NormalizeAssistantDraft applies the canonical Ticket validators to a draft
// and reports whether they changed it. The only normalization is the removal of
// surrounding blank space; nothing is summarized or truncated. An oversized or
// empty value is an error, never a silent cut.
func NormalizeAssistantDraft(title, body string) (normalizedTitle, normalizedBody string, changed bool, err error) {
	normalizedTitle, err = NormalizeTicketTitle(title)
	if err != nil {
		return "", "", false, err
	}
	normalizedBody, err = NormalizeTicketDescription(body)
	if err != nil {
		return "", "", false, err
	}
	if normalizedBody == "" {
		return "", "", false, invalidTicketField("description", "the draft is empty")
	}
	return normalizedTitle, normalizedBody, normalizedTitle != title || normalizedBody != body, nil
}

// SuggestAssistantDraftTitle derives a short, single-line title from a draft:
// the first sentence of its first line. It is only a starting point the user
// edits; it calls no model and never stands in for the body.
func SuggestAssistantDraftTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "#>*-•_ \t")
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		line = firstSentence(line)
		if utf8.RuneCountInString(line) > assistantDraftTitleChars {
			line = strings.TrimSpace(string([]rune(line)[:assistantDraftTitleChars])) + "…"
		}
		return line
	}
	return "Draft"
}

// firstSentence cuts a line after its first sentence. A very short opening
// such as "Dr." is not treated as a sentence.
func firstSentence(line string) string {
	const minimumRunes = 8
	runes := []rune(line)
	for i, r := range runes {
		if !strings.ContainsRune(".!?。！？", r) || i+1 < minimumRunes {
			continue
		}
		if i == len(runes)-1 || runes[i+1] == ' ' {
			return string(runes[:i+1])
		}
	}
	return line
}

// AssistantDraftDigest fingerprints exactly what a save commits: the target
// workspace and the normalized title and body.
func AssistantDraftDigest(workspaceID, title, body string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + title + "\x00" + body))
	return hex.EncodeToString(sum[:])[:assistantDraftDigestLength]
}

// SourceID renders the key as a Ticket source ID.
func (k AssistantDraftKey) SourceID() string {
	return strings.Join([]string{assistantDraftSourcePrefix, k.ConversationID, k.MessageID, k.OperationID, k.Digest}, ":")
}

func (k AssistantDraftKey) valid() bool {
	return isAssistantDraftToken(k.ConversationID) && isAssistantDraftToken(k.MessageID) &&
		isAssistantDraftToken(k.OperationID) && len(k.Digest) == assistantDraftDigestLength && isAssistantDraftToken(k.Digest)
}

// ParseAssistantDraftSourceID reads a saved draft's source key. It reports
// false for any other Ticket, including assistant captures that are not drafts.
func ParseAssistantDraftSourceID(sourceID string) (AssistantDraftKey, bool) {
	parts := strings.Split(sourceID, ":")
	if len(parts) != 5 || parts[0] != assistantDraftSourcePrefix {
		return AssistantDraftKey{}, false
	}
	key := AssistantDraftKey{ConversationID: parts[1], MessageID: parts[2], OperationID: parts[3], Digest: parts[4]}
	return key, key.valid()
}

// assistantDraftKeyOf returns the draft key of a record that is a saved draft.
func assistantDraftKeyOf(task *Task) (AssistantDraftKey, bool) {
	if task == nil || task.SourceType != TicketSourceAssistant {
		return AssistantDraftKey{}, false
	}
	return ParseAssistantDraftSourceID(task.SourceID)
}

func (s *AssistantDraftService) key(input AssistantDraftInput, title, body string) (AssistantDraftKey, error) {
	key := AssistantDraftKey{
		ConversationID: strings.TrimSpace(input.ConversationID),
		MessageID:      strings.TrimSpace(input.MessageID),
		OperationID:    strings.TrimSpace(input.OperationID),
		Digest:         AssistantDraftDigest(strings.TrimSpace(input.WorkspaceID), title, body),
	}
	if !key.valid() {
		return AssistantDraftKey{}, invalidTicketField("source_id", "the save is missing its conversation, message, or operation reference")
	}
	return key, nil
}

func (s *AssistantDraftService) receipt(ticket *Ticket, created bool, title, body string) *AssistantDraftReceipt {
	return &AssistantDraftReceipt{
		Ticket:  *ticket,
		Created: created,
		ChangedSince: !created &&
			(ticket.Title != title || ticket.Description != body || ticket.State != TicketStateBacklog),
	}
}

// Find returns the Ticket an earlier attempt of this exact save created, or
// nil when there is none. When the operation ID was used for something else it
// returns ErrAssistantDraftConflict together with the Ticket that earlier save
// created, so the caller can point at what is actually stored. It never writes.
func (s *AssistantDraftService) Find(input AssistantDraftInput) (*AssistantDraftReceipt, error) {
	title, body, _, err := NormalizeAssistantDraft(input.Title, input.Body)
	if err != nil {
		return nil, err
	}
	want, err := s.key(input, title, body)
	if err != nil {
		return nil, err
	}
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	ws, err := s.backlog.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	for i := range ws.Tasks {
		key, ok := assistantDraftKeyOf(&ws.Tasks[i])
		if !ok || key.OperationID != want.OperationID {
			continue
		}
		ticket := NewTicket(&ws.Tasks[i], ws.ID, ws.Name, ws.FolderSlug)
		if key != want {
			return s.receipt(&ticket, false, title, body), ErrAssistantDraftConflict
		}
		return s.receipt(&ticket, false, title, body), nil
	}
	return nil, nil
}

// Save captures the reviewed draft as one Backlog Ticket: unassigned,
// unscheduled, and not runnable. Repeating the same save — a double-click, a
// retry after a lost response, a retry after a restart — returns the Ticket the
// first attempt created, even if it has been edited since. The same operation
// ID with different content is refused.
func (s *AssistantDraftService) Save(input AssistantDraftInput) (*AssistantDraftReceipt, error) {
	title, body, _, err := NormalizeAssistantDraft(input.Title, input.Body)
	if err != nil {
		return nil, err
	}
	want, err := s.key(input, title, body)
	if err != nil {
		return nil, err
	}
	ticket, created, err := s.backlog.tickets().createIdempotent(TicketCreateInput{
		WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		State:       TicketStateBacklog,
		Title:       title,
		Description: body,
		Source:      TicketSourceAssistant,
		SourceID:    want.SourceID(),
		Actor:       TicketActorUser,
		ActorID:     strings.TrimSpace(input.ActorID),
	}, func(candidate, _ *Task) (bool, error) {
		key, ok := assistantDraftKeyOf(candidate)
		if !ok || key.OperationID != want.OperationID {
			return false, nil
		}
		if key != want {
			return false, ErrAssistantDraftConflict
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		s.backlog.renderAfterMutation(ticket.OwningWorkspaceID)
	}
	return s.receipt(ticket, created, title, body), nil
}
