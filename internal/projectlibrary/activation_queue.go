package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ActivationQueue persists only order, per-item skip/connection receipts and
// navigation progress. It grants no consent to create a child: every song
// still needs the independent activation review and explicit confirmation.
// A browser's lost pending key can never be reconstructed as user approval.
type ActivationQueue struct {
	ID        string    `json:"id"`
	IDs       []string  `json:"ids"`
	Index     int       `json:"index"`
	Skipped   []string  `json:"skipped,omitempty"`
	Connected []string  `json:"connected,omitempty"`
	Revision  int64     `json:"revision"`
	Status    string    `json:"status"` // active, complete, discarded
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (q ActivationQueue) valid() bool {
	if q.ID == "" || !validText(q.ID, 160) || len(q.IDs) < 2 || len(q.IDs) > 100 ||
		q.Revision < 1 || q.Index < 0 || q.Index > len(q.IDs) ||
		q.CreatedAt.IsZero() || q.UpdatedAt.Before(q.CreatedAt) || !q.ExpiresAt.After(q.CreatedAt) ||
		len(q.Skipped)+len(q.Connected) != q.Index {
		return false
	}
	switch q.Status {
	case "active":
		if q.Index == len(q.IDs) {
			return false
		}
	case "complete":
		if q.Index != len(q.IDs) {
			return false
		}
	case "discarded":
	default:
		return false
	}
	ids, handled := map[string]bool{}, map[string]bool{}
	for _, id := range q.IDs {
		if id == "" || !validText(id, 160) || ids[id] {
			return false
		}
		ids[id] = true
	}
	for _, id := range append(append([]string{}, q.Skipped...), q.Connected...) {
		if !ids[id] || handled[id] {
			return false
		}
		handled[id] = true
	}
	for i, id := range q.IDs {
		if handled[id] != (i < q.Index) {
			return false
		}
	}
	return true
}

// ActivationQueueOutcome deliberately omits entry IDs and source evidence.
// A forgotten Home record cannot be reconstructed from earlier queue history.
type ActivationQueueOutcome struct {
	ID             string    `json:"id"`
	Status         string    `json:"status"` // complete, discarded
	SelectedCount  int       `json:"selected_count"`
	SkippedCount   int       `json:"skipped_count"`
	ConnectedCount int       `json:"connected_count"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
}

const maxQueueOutcomes = 16

func (o ActivationQueueOutcome) valid() bool {
	if o.ID == "" || !validText(o.ID, 160) || o.SelectedCount < 2 || o.SelectedCount > 100 ||
		o.SkippedCount < 0 || o.ConnectedCount < 0 || o.SkippedCount+o.ConnectedCount > o.SelectedCount ||
		o.StartedAt.IsZero() || o.FinishedAt.Before(o.StartedAt) {
		return false
	}
	return (o.Status == "complete" && o.SkippedCount+o.ConnectedCount == o.SelectedCount) || o.Status == "discarded"
}

func (q ActivationQueue) outcome() ActivationQueueOutcome {
	return ActivationQueueOutcome{ID: q.ID, Status: q.Status, SelectedCount: len(q.IDs),
		SkippedCount: len(q.Skipped), ConnectedCount: len(q.Connected), StartedAt: q.CreatedAt, FinishedAt: q.UpdatedAt}
}

// archiveTerminalQueue drops the detailed IDs once no navigation is left. It
// runs when another queue begins and when an owner forgets one of its entries.
// Active queues retain pending IDs until the owner explicitly skips/discards.
func archiveTerminalQueue(doc *Document) {
	if doc.Queue == nil || doc.Queue.Status == "active" {
		return
	}
	doc.QueueHistory = append(doc.QueueHistory, doc.Queue.outcome())
	if len(doc.QueueHistory) > maxQueueOutcomes {
		doc.QueueHistory = doc.QueueHistory[len(doc.QueueHistory)-maxQueueOutcomes:]
	}
	doc.Queue = nil
}

// A skipped item is no longer needed to navigate an active queue. Forget
// replaces its durable queue identifier with a non-catalog placeholder while
// preserving only the skip count/order. Future items stay pending until the
// owner explicitly skips or discards the queue.
func redactHandledQueueEntry(doc *Document, entryID string, at time.Time) {
	q := doc.Queue
	if q == nil || q.Status != "active" {
		return
	}
	for i := 0; i < q.Index; i++ {
		if q.IDs[i] != entryID {
			continue
		}
		found := false
		for _, skipped := range q.Skipped {
			if skipped == entryID {
				found = true
				break
			}
		}
		if !found {
			return
		} // a connected entry cannot be forgotten here
		var alias string
		for {
			alias = newID()
			if !slices.Contains(q.IDs, alias) {
				break
			}
		}
		q.IDs[i] = alias
		for j := range q.Skipped {
			if q.Skipped[j] == entryID {
				q.Skipped[j] = alias
				break
			}
		}
		q.Revision++
		q.UpdatedAt = at
		return
	}
}

type ActivationQueueView struct {
	Revision int64                    `json:"revision"`
	Queue    *ActivationQueue         `json:"queue,omitempty"`
	Recent   []ActivationQueueOutcome `json:"recent,omitempty"`
}

func queueDigest(scope Scope, action, id, entryID string, revision int64, ids []string) string {
	data, _ := json.Marshal(struct {
		Scope    Scope    `json:"scope"`
		Action   string   `json:"action"`
		ID       string   `json:"id"`
		EntryID  string   `json:"entry_id"`
		Revision int64    `json:"revision"`
		IDs      []string `json:"ids,omitempty"`
	}{scope, action, id, entryID, revision, ids})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) CurrentActivationQueue(scope Scope) (ActivationQueueView, error) {
	doc, _, err := s.readSnapshot(scope)
	if err != nil {
		return ActivationQueueView{}, err
	}
	view := ActivationQueueView{Revision: doc.Revision}
	for i := len(doc.QueueHistory) - 1; i >= 0 && len(view.Recent) < 5; i-- {
		view.Recent = append(view.Recent, doc.QueueHistory[i])
	}
	if doc.Queue != nil && doc.Queue.Status != "active" {
		view.Recent = append([]ActivationQueueOutcome{doc.Queue.outcome()}, view.Recent...)
		if len(view.Recent) > 5 {
			view.Recent = view.Recent[:5]
		}
	}
	if doc.Queue != nil && doc.Queue.Status == "active" {
		q := *doc.Queue
		q.IDs = append([]string(nil), q.IDs...)
		q.Skipped = append([]string(nil), q.Skipped...)
		q.Connected = append([]string(nil), q.Connected...)
		if !q.ExpiresAt.After(s.now().UTC()) {
			q.Status = "expired"
		}
		view.Queue = &q
	}
	return view, nil
}

// StartActivationQueue is an explicit owner action, not a bulk activation.
// All IDs must be distinct, current catalog-only entries in this exact Home.
func (s *Store) StartActivationQueue(scope Scope, ids []string, key string) (ActivationQueue, bool, error) {
	if len(ids) < 2 || len(ids) > 100 || key == "" || !validText(key, 160) {
		return ActivationQueue{}, false, ErrConflict
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || !validText(id, 160) || seen[id] {
			return ActivationQueue{}, false, ErrConflict
		}
		seen[id] = true
	}
	doc, _, err := s.readSnapshot(scope)
	if err != nil {
		return ActivationQueue{}, false, err
	}
	digest := queueDigest(scope, "start_activation_queue", "", "", 0, ids)
	for _, op := range doc.Operations {
		if op.Key == key {
			if op.Action == "start_activation_queue" && op.Digest == digest && doc.Queue != nil && doc.Queue.ID == op.ConsequenceID {
				return *doc.Queue, true, nil
			}
			return ActivationQueue{}, false, ErrConflict
		}
	}
	if doc.Queue != nil && doc.Queue.Status == "active" {
		return ActivationQueue{}, false, ErrConflict
	}
	for _, id := range ids {
		entry := sessionEntry(doc, id)
		if entry == nil || entry.Link != nil {
			return ActivationQueue{}, false, ErrConflict
		}
	}
	at := s.now().UTC()
	queue := ActivationQueue{ID: newID(), IDs: append([]string(nil), ids...), Revision: 1, Status: "active",
		CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour)}
	receipt, replay, err := s.mutate(scope, doc.Revision, operation{key: key, action: "start_activation_queue", digest: digest},
		func(current *Document) (string, error) {
			if current.Queue != nil && current.Queue.Status == "active" {
				return "", ErrConflict
			}
			for _, id := range ids {
				entry := sessionEntry(*current, id)
				if entry == nil || entry.Link != nil {
					return "", ErrConflict
				}
			}
			archiveTerminalQueue(current)
			current.Queue = &queue
			return queue.ID, nil
		})
	if err != nil {
		return ActivationQueue{}, false, err
	}
	if replay {
		fresh, readErr := s.Read(scope)
		if readErr == nil && fresh.Queue != nil && fresh.Queue.ID == receipt.ConsequenceID {
			return *fresh.Queue, true, nil
		}
		return ActivationQueue{}, false, ErrConflict
	}
	return queue, false, nil
}

// ProgressActivationQueue acknowledges exactly one current item. Skip creates
// nothing; connected advancement requires current Home/child reciprocal link
// and locator evidence, never a browser assertion that a creator succeeded.
func (s *Store) ProgressActivationQueue(scope Scope, queueID, entryID, action, key string, expected int64) (ActivationQueue, bool, error) {
	if queueID == "" || entryID == "" || key == "" || !validText(queueID, 160) || !validText(entryID, 160) ||
		!validText(key, 160) || expected < 1 || (action != "skip" && action != "connected") {
		return ActivationQueue{}, false, ErrConflict
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ActivationQueue{}, false, err
	}
	digest := queueDigest(scope, "queue_"+action, queueID, entryID, expected, nil)
	for _, op := range doc.Operations {
		if op.Key == key {
			if op.Action == "queue_"+action && op.Digest == digest && op.ConsequenceID == entryID &&
				doc.Queue != nil && doc.Queue.ID == queueID {
				return *doc.Queue, true, nil
			}
			return ActivationQueue{}, false, ErrConflict
		}
	}
	if doc.Queue == nil || doc.Queue.ID != queueID || doc.Queue.Status != "active" ||
		doc.Queue.Revision != expected || !doc.Queue.ExpiresAt.After(s.now().UTC()) ||
		doc.Queue.IDs[doc.Queue.Index] != entryID {
		return ActivationQueue{}, false, ErrConflict
	}
	if action == "connected" && !s.queueLinkCurrent(scope, doc, state, entryID) {
		return ActivationQueue{}, false, ErrConflict
	}
	var updated ActivationQueue
	_, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: key, action: "queue_" + action, digest: digest},
		func(current *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
			if action != "connected" {
				return true
			}
			entry := sessionEntry(doc, entryID)
			if entry == nil || entry.Link == nil {
				return false
			}
			for _, id := range current.LinkedProjectIDs {
				if id == entry.Link.WorkspaceID {
					return true
				}
			}
			return false
		}, func(current *Document) (string, error) {
			q := current.Queue
			if q == nil || q.ID != queueID || q.Status != "active" || q.Revision != expected ||
				!q.ExpiresAt.After(s.now().UTC()) || q.IDs[q.Index] != entryID {
				return "", ErrConflict
			}
			if action == "skip" {
				q.Skipped = append(q.Skipped, entryID)
			} else {
				entry := sessionEntry(*current, entryID)
				if entry == nil || entry.Link == nil || !slices.Contains(state.LinkedProjectIDs, entry.Link.WorkspaceID) {
					return "", ErrConflict
				}
				q.Connected = append(q.Connected, entryID)
			}
			q.Index++
			q.Revision++
			q.UpdatedAt = s.now().UTC()
			if q.Index == len(q.IDs) {
				q.Status = "complete"
			}
			updated = *q
			return entryID, nil
		})
	return updated, replay, err
}

func (s *Store) queueLinkCurrent(scope Scope, doc Document, state *workspace.AssistantProgramState, entryID string) bool {
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Link == nil {
		return false
	}
	_, _, _, link, err := s.exactLinkedChild(scope, state, entry.Link.WorkspaceID)
	return err == nil && link == *entry.Link
}

func (s *Store) DiscardActivationQueue(scope Scope, queueID, key string, expected int64) (bool, error) {
	if queueID == "" || key == "" || !validText(queueID, 160) || !validText(key, 160) || expected < 1 {
		return false, ErrConflict
	}
	doc, _, err := s.readSnapshot(scope)
	if err != nil {
		return false, err
	}
	digest := queueDigest(scope, "discard_activation_queue", queueID, "", expected, nil)
	for _, op := range doc.Operations {
		if op.Key == key {
			if op.Action == "discard_activation_queue" && op.Digest == digest && op.ConsequenceID == queueID {
				return true, nil
			}
			return false, ErrConflict
		}
	}
	if doc.Queue == nil || doc.Queue.ID != queueID || doc.Queue.Status != "active" || doc.Queue.Revision != expected {
		return false, ErrConflict
	}
	_, replay, err := s.mutate(scope, doc.Revision, operation{key: key, action: "discard_activation_queue", digest: digest},
		func(current *Document) (string, error) {
			if current.Queue == nil || current.Queue.ID != queueID || current.Queue.Revision != expected || current.Queue.Status != "active" {
				return "", ErrConflict
			}
			current.Queue.Status = "discarded"
			current.Queue.Revision++
			current.Queue.UpdatedAt = s.now().UTC()
			return queueID, nil
		})
	return replay, err
}
