package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

var ErrArrangementUpgradeRequired = errors.New("saving an arrangement upgrades this Home library; older Ori versions cannot open it")

// Arrangement owns one inert record in the canonical Home envelope. SourceRevision
// records the evidence seen by the planner; CatalogRevision pins the envelope
// AFTER this save. Its own write therefore does not immediately stale the draft.
// Later catalog changes require fresh review. Neither pin is approval.
type Arrangement struct {
	RevisionDiff       *ArrangementRevisionDiff    `json:"revision_diff,omitempty"`
	RevisionDiscussion *RevisionDiscussion         `json:"revision_discussion,omitempty"`
	Record             workspacearrangement.Record `json:"record"`
	CatalogRevision    int64                       `json:"catalog_revision"`
	ManagerInstanceID  string                      `json:"manager_instance_id"`
	ManagerName        string                      `json:"manager_name"`
	BindingDigest      string                      `json:"binding_digest"`
	// Empty Kind denotes a proposal for compatibility with existing v2 records.
	Kind                 string                   `json:"kind,omitempty"`
	Clarification        string                   `json:"clarification,omitempty"`
	CollectionReceipt    *CollectionCommitReceipt `json:"collection_receipt,omitempty"`
	CollectionCorrection *CollectionCorrection    `json:"collection_correction,omitempty"`
}

func (d Document) validArrangements() bool {
	if d.SchemaVersion == SchemaVersion {
		return len(d.Arrangements) == 0 && len(d.Collections) == 0
	}
	if len(d.Arrangements) > workspacearrangement.MaxContextRecords || len(d.Collections) > 100 {
		return false
	}
	entries := map[string]bool{}
	for _, entry := range d.Entries {
		entries[entry.ID] = true
	}
	collectionIDs := map[string]bool{}
	memberships := 0
	for _, collection := range d.Collections {
		if collectionIDs[collection.ID] || !collection.valid(entries) {
			return false
		}
		collectionIDs[collection.ID] = true
		memberships += len(collection.EntryIDs)
	}
	if memberships > workspacearrangement.MaxMemberships {
		return false
	}
	ids := map[string]bool{}
	for _, saved := range d.Arrangements {
		r := saved.Record
		if !saved.validOutcome() || ids[r.ID] || r.Validate() != nil || r.SourceKind != workspacearrangement.HomeLibrary || r.SourceID != d.HomeID ||
			saved.CatalogRevision < 1 || saved.CatalogRevision > d.Revision || saved.ManagerInstanceID == "" || saved.ManagerName == "" ||
			!validText(saved.ManagerInstanceID, 160) || !validText(saved.ManagerName, 160) || !validDigest(saved.BindingDigest) {
			return false
		}
		if !validCollectionReceipt(saved, d.Revision) || !collectionWitnessesMatch(d, saved) || !validCollectionCorrection(saved) || !validRevisionDiscussion(saved) || !validArrangementRevisionDiff(saved) {
			return false
		}
		ids[r.ID] = true
		for id := range r.ItemRevisions {
			if !entries[id] {
				return false
			}
		}
		for id := range r.CollectionRevisions {
			if !collectionIDs[id] && r.State != "stale" && r.State != "completed" {
				return false
			}
		}
	}
	for _, collection := range d.Collections {
		if collection.ArrangementID != "" && !ids[collection.ArrangementID] {
			return false
		}
	}
	return true
}

func arrangementBinding(state *workspace.AssistantProgramState, authority ManagerAuthority) string {
	encoded, err := json.Marshal(struct {
		Manager  ManagerAuthority
		Bindings workspace.AssistantRoleBindingSet
		Provider *workspace.AssistantProgramHomeOwner
	}{authority, state.HomeBindings, state.HomeProvider})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// SaveArrangement is an explicit host completion, never a model tool. It does
// not initialize a library, create a collection, scan, or activate anything.
// allowUpgrade must come from the disclosed organizing mode's user consent.
func (s *Store) SaveArrangement(ctx context.Context, owner string, authority ManagerAuthority, expected int64, allowUpgrade bool, record workspacearrangement.Record) (Arrangement, bool, error) {
	return s.saveArrangementOutcome(ctx, owner, authority, expected, allowUpgrade, record, "", "")
}

func (s *Store) saveArrangementOutcome(ctx context.Context, owner string, authority ManagerAuthority, expected int64, allowUpgrade bool, record workspacearrangement.Record, kind, clarification string) (Arrangement, bool, error) {
	if err := ctx.Err(); err != nil {
		return Arrangement{}, false, err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return Arrangement{}, false, ErrCorrupt
	}
	record, err = workspacearrangement.DecodeRecord(encoded)
	if err != nil || record.SourceKind != workspacearrangement.HomeLibrary || record.SourceID != authority.HomeID || record.Revision != 1 ||
		(record.State != "draft" && record.State != "needs_more_evidence") {
		return Arrangement{}, false, ErrCorrupt
	}
	outcome := Arrangement{Record: record, Kind: kind, Clarification: clarification}
	if !outcome.validOutcome() {
		return Arrangement{}, false, ErrCorrupt
	}
	// Reserve the slightly larger stale (false) catalog-current projection.
	if _, err := arrangementHistory(outcome, -1); err != nil {
		return Arrangement{}, false, err
	}
	// Preserve existing proposal replay digests. A clarification's text and
	// kind are part of its identity, never an independently editable transcript.
	if kind != "" {
		encoded, err = json.Marshal(struct {
			Record              workspacearrangement.Record
			Kind, Clarification string
		}{record, kind, clarification})
		if err != nil || len(encoded) > workspacearrangement.MaxRecordBytes {
			return Arrangement{}, false, ErrLimit
		}
	}
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return Arrangement{}, false, err
	}
	if scope.OwnerUserID != owner {
		return Arrangement{}, false, ErrUnavailable
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return Arrangement{}, false, err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	// Replay returns canonical history, including a redacted tombstone; it
	// never reinstates original membership after forget or a later edit.
	for _, prior := range doc.Operations {
		if prior.Key != record.ID {
			continue
		}
		if prior.Action != "save_arrangement" || prior.Digest != digest || prior.ConsequenceID != record.ID {
			return Arrangement{}, false, ErrConflict
		}
		for _, saved := range doc.Arrangements {
			if saved.Record.ID == record.ID {
				return saved, true, nil
			}
		}
		return Arrangement{}, false, ErrConflict
	}
	if expected != doc.Revision {
		return Arrangement{}, false, ErrConflict
	}
	if doc.SchemaVersion == SchemaVersion && !allowUpgrade {
		return Arrangement{}, false, ErrArrangementUpgradeRequired
	}
	evidence, err := workspacearrangement.NewEvidence(ctx, func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return s.ArrangementEvidence(ctx, owner, authority)
	})
	if err != nil {
		return Arrangement{}, false, err
	}
	if err := evidence.ValidateRecord(ctx, record); err != nil {
		return Arrangement{}, false, ErrConflict
	}
	pin := arrangementBinding(state, authority)
	var result Arrangement
	_, replay, err := s.mutateWithHomePolicy(scope, expected, operation{key: record.ID, action: "save_arrangement", digest: digest},
		func(current *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return ctx.Err() == nil && boundManager(current, home, authority) && arrangementBinding(current, authority) == pin
		}, func(current *Document) (string, error) {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if len(current.Arrangements) >= workspacearrangement.MaxContextRecords {
				return "", ErrLimit
			}
			for _, saved := range current.Arrangements {
				if saved.Record.ID == record.ID {
					return "", ErrConflict
				}
			}
			// A stale item/collection revision is not repaired by changing intent.
			for id, revision := range record.ItemRevisions {
				entry := sessionEntry(*current, id)
				if entry == nil || fmt.Sprintf("%d-%d", entry.Revision, entry.Fields.Revision) != revision {
					return "", ErrConflict
				}
			}
			current.SchemaVersion = ArrangementSchemaVersion
			result = Arrangement{Record: record, CatalogRevision: current.Revision + 1, ManagerInstanceID: authority.AgentInstanceID, ManagerName: authority.AgentName, BindingDigest: pin, Kind: kind, Clarification: clarification}
			current.Arrangements = append(current.Arrangements, result)
			return record.ID, nil
		})
	if err != nil {
		return Arrangement{}, false, err
	}
	if replay {
		// Another completion won after our initial read. Fetch its record rather
		// than returning a zero value or asserting our candidate was saved.
		fresh, err := s.Read(scope)
		if err != nil {
			return Arrangement{}, true, err
		}
		for _, saved := range fresh.Arrangements {
			if saved.Record.ID == record.ID {
				return saved, true, nil
			}
		}
		return Arrangement{}, true, ErrConflict
	}
	return result, false, nil
}

// forgetArrangements removes exact references and conservatively clears EVERY
// draft's generated text: even a draft without an explicit reference could name
// the forgotten entry in its goal/rationale. Opaque IDs/digests remain for replay.
func forgetArrangements(doc *Document, entryID string, now time.Time) {
	for i := range doc.Collections {
		collection := &doc.Collections[i]
		if slices.Contains(collection.EntryIDs, entryID) {
			collection.EntryIDs = slices.DeleteFunc(collection.EntryIDs, func(id string) bool { return id == entryID })
			collection.Revision++
			collection.UpdatedAt = now
		}
	}
	for i := range doc.Arrangements {
		doc.Arrangements[i].Clarification = ""
		doc.Arrangements[i].RevisionDiscussion = nil
		record := &doc.Arrangements[i].Record
		record.State, record.Revision, record.UpdatedAt = "stale", record.Revision+1, now
		record.Draft = workspacearrangement.Draft{Version: workspacearrangement.Version, SourceRevision: record.Draft.SourceRevision, Goal: "Catalog history was forgotten. Request a new arrangement."}
		record.ItemRevisions, record.CollectionRevisions = nil, nil
		record.Coverage.Note = "Historical coverage redacted after a catalog entry was forgotten."
	}
}
