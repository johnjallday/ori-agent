package personalassistant

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func knowledgeContinuityFixture(t *testing.T) []byte {
	t.Helper()
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	doc := KnowledgeDocument{SchemaVersion: KnowledgeSchemaVersion, Version: 3,
		Owner: KnowledgeOwner{UserID: "local", AssistantID: "assistant-1", HQWorkspaceID: "hq-1", HQFolderSlug: "my-hq", EntryAgentInstanceID: "entry-1"},
		Items: []KnowledgeItem{
			{ID: "memory-fact", Version: 1, State: KnowledgeApproved, SemanticKey: "garden.sun", Category: "projects", CurrentRevisionID: "r1",
				Revisions: []KnowledgeRevision{{ID: "r1", Text: "Tomatoes need full sun.", CreatedAt: at}},
				Target:    &KnowledgeTarget{Kind: "memory", Marker: "m1", CanonicalHash: "hash-1"}},
			{ID: "profile-fact", Version: 1, State: KnowledgeApproved, SemanticKey: "user.style", Category: "how_you_work", CurrentRevisionID: "r2",
				Revisions: []KnowledgeRevision{{ID: "r2", Text: "Prefers short answers.", CreatedAt: at}},
				Target:    &KnowledgeTarget{Kind: "profile", Field: "preferences.response_style", CanonicalHash: "hash-2"}},
			{ID: "mid-write", Version: 1, State: KnowledgeCandidate, SemanticKey: "garden.beds", Category: "projects",
				Prepared: &KnowledgeOperation{RequestID: "req-1", Kind: "approve"}},
			{ID: "rejected", Version: 1, State: KnowledgeRejected, SemanticKey: "garden.pests", Category: "projects"},
			{ID: "forgotten", Version: 2, State: KnowledgeForgotten, SemanticKey: "user.old-address", Category: "people"},
		},
		Tombstones: []KnowledgeTombstone{{SemanticKey: "user.old-address", ItemID: "forgotten", CreatedAt: at}},
	}
	data, err := encodeKnowledge(doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// An adopted HQ's knowledge is rebound to this installation's folder name.
// What was forgotten or rejected stays so; a value that lived in the source's
// global profile, or was mid-write, needs review here rather than arriving
// approved.
func TestProjectContinuityKnowledgeRebindsAndNeverResurrects(t *testing.T) {
	data := knowledgeContinuityFixture(t)
	binding := KnowledgeBinding{UserID: "local", AssistantID: "assistant-1", HQWorkspaceID: "hq-1", HQFolderSlug: "my-hq-2", EntryAgentInstanceID: "entry-1"}
	projected, summary, err := ProjectContinuityKnowledge(data, binding)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeKnowledgeSidecar(projected)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Owner.HQFolderSlug != "my-hq-2" {
		t.Fatalf("owner not rebound to the local folder: %+v", doc.Owner)
	}
	states := map[string]KnowledgeState{}
	for _, item := range doc.Items {
		states[item.ID] = item.State
		if item.Prepared != nil {
			t.Fatalf("a source-side prepared write travelled as pending work: %s", item.ID)
		}
	}
	want := map[string]KnowledgeState{"memory-fact": KnowledgeApproved, "profile-fact": KnowledgeNeedsReview,
		"mid-write": KnowledgeNeedsReview, "rejected": KnowledgeRejected, "forgotten": KnowledgeForgotten}
	for id, state := range want {
		if states[id] != state {
			t.Fatalf("%s: got %s want %s", id, states[id], state)
		}
	}
	if len(doc.Tombstones) != 1 || summary.Forgotten != 1 || summary.Rejected != 1 || summary.NeedsReview != 2 || summary.Unfinished != 0 {
		t.Fatalf("summary/tombstones: %+v %+v", summary, doc.Tombstones)
	}

	// Knowledge of another assistant is never rebound onto this one.
	other := binding
	other.AssistantID = "someone-else"
	if _, _, err := ProjectContinuityKnowledge(data, other); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("foreign knowledge was rebound", err)
	}
}

// The checkpoint carries counts and owner identity only, never remembered text.
func TestContinuityKnowledgeSummaryCarriesNoText(t *testing.T) {
	doc, err := decodeKnowledgeSidecar(knowledgeContinuityFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	record, err := workspacecontinuity.EncodeRecord("hq-1", summarizeKnowledge("hq-1", doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Tomatoes", "short answers", "old-address"} {
		if strings.Contains(string(record.Data), text) {
			t.Fatalf("summary leaked remembered text %q", text)
		}
	}
	decoded, err := DecodeContinuityKnowledge(record, "hq-1")
	if err != nil || decoded.Items != 5 || decoded.Approved != 2 {
		t.Fatalf("summary: %+v %v", decoded, err)
	}
}
