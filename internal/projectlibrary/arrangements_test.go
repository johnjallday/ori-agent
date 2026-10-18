package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func homeArrangementFixture(t *testing.T) (*runFixture, ManagerAuthority, workspacearrangement.Record) {
	t.Helper()
	f := newRunFixture(t)
	authority := ManagerAuthority{HomeID: f.scope.HomeID, AgentInstanceID: managerBinding.AgentInstanceID, AgentName: managerBinding.AgentName}
	e, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := e.Select(t.Context(), workspacearrangement.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	draft := workspacearrangement.Draft{Version: 1, SourceRevision: selection.Revision, Goal: "Keep projects catalog-only", Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: "Client references", Members: workspacearrangement.Selection{Handle: selection.Handle}}}}
	raw, _ := json.Marshal(draft)
	resolved, err := e.Resolve(t.Context(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.NewRecord(t.Context(), resolved, workspacearrangement.Provenance{Origin: "model", SourceID: f.scope.HomeID, Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return f, authority, record
}

func TestHomeArrangement_ExplicitUpgradePreservesCatalogAndReplaysCanonicalRecord(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	before := f.doc(t)
	if _, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, before.Revision, false, record); !errors.Is(err, ErrArrangementUpgradeRequired) {
		t.Fatal("undisclosed upgrade accepted", err)
	}
	if after := f.doc(t); !reflect.DeepEqual(after, before) {
		t.Fatal("declined upgrade wrote library")
	}
	saved, replay, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, record)
	if err != nil || replay || saved.CatalogRevision != before.Revision+1 {
		t.Fatalf("save: %+v %v %v", saved, replay, err)
	}
	after := f.doc(t)
	if after.SchemaVersion != 2 || len(after.Arrangements) != 1 || len(after.Collections) != 0 || saved.CatalogRevision != after.Revision {
		t.Fatal("save created effects or immediately invalidated own pin")
	}
	if !reflect.DeepEqual(before.Roots, after.Roots) || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Sessions, after.Sessions) || !reflect.DeepEqual(before.Proposals, after.Proposals) {
		t.Fatal("upgrade altered existing library")
	}
	again, replay, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, record)
	if err != nil || !replay || !reflect.DeepEqual(saved, again) || !reflect.DeepEqual(f.doc(t), after) {
		t.Fatal("replay changed canonical state", err)
	}
	changed := record
	changed.Draft.Goal = "Different goal"
	if _, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, before.Revision, true, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("idempotency collision accepted", err)
	}
	// A fresh store reads v2 without migration or mutation.
	restarted := NewStore(f.file)
	loaded, err := restarted.Read(f.scope)
	if err != nil || !reflect.DeepEqual(loaded, after) {
		t.Fatal("restart lost v2 data", err)
	}
}

func TestHomeArrangement_RefusesStaleForeignUnavailableAndAbsentLibrary(t *testing.T) {
	for _, failure := range []string{"owner", "manager", "provider", "revision", "source", "absent", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			f, authority, record := homeArrangementFixture(t)
			before := f.doc(t)
			owner, expected := f.scope.OwnerUserID, before.Revision
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch failure {
			case "owner":
				owner = "foreign"
			case "manager":
				authority.AgentInstanceID = "foreign"
			case "provider":
				f.available.Store(false)
			case "revision":
				expected--
			case "source":
				record.Draft.SourceRevision = "invented"
			case "absent":
				if err := f.file.Update(f.scope.HomeID, func(home *workspace.Workspace) error {
					state := home.GetAssistantProgramState()
					state.ProjectLibrary = nil
					home.SetAssistantProgramState(state)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			if _, _, err := f.library.SaveArrangement(ctx, owner, authority, expected, true, record); err == nil {
				t.Fatal("invalid save accepted")
			}
			if failure == "absent" {
				if _, err := f.library.Read(f.scope); !errors.Is(err, ErrNotInitialized) {
					t.Fatal("save initialized absent library", err)
				}
			} else if !reflect.DeepEqual(f.doc(t), before) {
				t.Fatal("failed save wrote library")
			}
		})
	}
}

func TestHomeArrangement_V1RejectsExtensionsEvenEmptyAndV2ValidatesReferences(t *testing.T) {
	f, _, _ := homeArrangementFixture(t)
	doc := f.doc(t)
	raw, _ := json.Marshal(doc)
	for _, field := range []string{`"arrangements":[]`, `"collections":null`, `"Collections":[]`} {
		if _, err := decodeDocument([]byte(strings.TrimSuffix(string(raw), "}")+","+field+"}"), f.scope); !errors.Is(err, ErrCorrupt) {
			t.Fatal("v1 silently accepted v2 field", field, err)
		}
	}
	doc.SchemaVersion = ArrangementSchemaVersion
	entryID := doc.Entries[0].ID
	at := f.library.now().UTC()
	doc.Collections = []LogicalCollection{{ID: "collection", Name: "References", Revision: 1, EntryIDs: []string{entryID}, Author: "Manager", Origin: "manual", CreatedAt: at, UpdatedAt: at}}
	if !doc.valid(f.scope) {
		t.Fatal("valid v2 references rejected")
	}
	doc.Collections[0].EntryIDs = []string{entryID, entryID}
	if doc.valid(f.scope) {
		t.Fatal("duplicate membership accepted")
	}
	doc.Collections[0].EntryIDs = []string{"foreign"}
	if doc.valid(f.scope) {
		t.Fatal("foreign membership accepted")
	}
	doc.SchemaVersion = 1
	if doc.valid(f.scope) {
		t.Fatal("downgrade retained hidden v2 state")
	}
}

func TestHomeArrangement_ForgetRedactsDraftsAndMembershipInSameCAS(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	doc := f.doc(t)
	entryID := record.Draft.Operations[0].Members.IDs[0]
	secret := "FORGOTTEN-LABEL-UNIQUE"
	record.Draft.Goal, record.Draft.Operations[0].Name = secret, secret
	if _, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, doc.Revision, true, record); err != nil {
		t.Fatal(err)
	}
	doc = f.doc(t)
	at := f.library.now().UTC()
	// Fixture setup, not a public collection writer. Revoke sources so the
	// existing explicit forget review is applicable without any filesystem IO.
	_, _, err := f.library.mutate(f.scope, doc.Revision, operation{key: "forget-fixture", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		for i := range current.Roots {
			current.Roots[i].RevokedAt = &at
		}
		current.Collections = []LogicalCollection{{ID: "collection", Name: "Independent client group", Revision: 1, EntryIDs: []string{entryID}, Author: "Manager", Origin: "manual", CreatedAt: at, UpdatedAt: at}}
		return "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	doc = f.doc(t)
	review, err := f.library.ReviewForget(f.scope, entryID, doc.Revision)
	if err != nil || review.CollectionCount != 1 || review.ArrangementCount != 1 {
		t.Fatalf("missing forget disclosure: %+v %v", review, err)
	}
	if _, err := f.library.CommitForget(f.scope, entryID, review.Token, "forget-apply"); err != nil {
		t.Fatal(err)
	}
	after := f.doc(t)
	encoded, _ := json.Marshal(after)
	if strings.Contains(string(encoded), secret) || len(after.Collections[0].EntryIDs) != 0 || after.Arrangements[0].Record.State != "stale" || len(after.Arrangements[0].Record.ItemRevisions) != 0 {
		t.Fatal("forget retained recoverable draft text or membership")
	}
	history, err := f.library.ReadArrangementHistory(t.Context(), f.scope, record.ID)
	if err != nil || history.CatalogCurrent || history.Arrangement.ReviewAvailable || history.Arrangement.State != "stale" || history.Arrangement.Operations != 0 {
		t.Fatalf("forgotten history revived authority: %+v %v", history, err)
	}
	preview, _ := json.Marshal(history)
	if strings.Contains(string(preview), secret) || strings.Contains(string(preview), entryID) {
		t.Fatal("history recovered forgotten metadata")
	}
	// Replaying the original save may return only the canonical redacted row.
	again, replay, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, doc.Revision, true, record)
	if err != nil || !replay || again.Record.State != "stale" || again.Record.Draft.Goal == secret || len(again.Record.Draft.Operations) != 0 {
		t.Fatal("replay resurrected forgotten draft", err)
	}
}
