package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestHomeArrangement_HistoryRemainsBoundedReadOnlyWithoutProviderOrSource(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	if refs, err := f.library.ArrangementRefs(t.Context(), f.scope); err != nil || len(refs) != 0 {
		t.Fatalf("v1 history: %v %v", refs, err)
	}
	if f.doc(t).SchemaVersion != SchemaVersion {
		t.Fatal("history read upgraded v1")
	}
	if _, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record); err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	f.available.Store(false)
	if err := os.Rename(f.tree.root, f.tree.root+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(f.tree.root+"-offline", f.tree.root) }()
	// A restarted read-only store has no provider witness or Manager callback.
	reader := NewStore(f.file)
	refs, err := reader.ArrangementRefs(t.Context(), f.scope)
	if err != nil || len(refs) != 1 || refs[0].ID != record.ID || !refs[0].CatalogCurrent || refs[0].ReviewAvailable {
		t.Fatalf("history references: %+v %v", refs, err)
	}
	history, err := reader.ReadArrangementHistory(t.Context(), f.scope, record.ID)
	if err != nil || history.Arrangement.ReviewAvailable || !history.CatalogCurrent || history.Arrangement.Goal != record.Draft.Goal {
		t.Fatalf("saved history: %+v %v", history, err)
	}
	raw, err := json.Marshal(history)
	if err != nil || len(raw) > 4096 || strings.Contains(string(raw), f.tree.root) || strings.Contains(string(raw), record.ProviderFingerprint) {
		t.Fatal("history exposed source/authority", err)
	}
	for id := range record.ItemRevisions {
		if strings.Contains(string(raw), id) {
			t.Fatal("history returned full membership")
		}
	}
	for _, field := range []string{"owner", "home"} {
		foreign := f.scope
		if field == "owner" {
			foreign.OwnerUserID = "foreign"
		} else {
			foreign.HomeID = "foreign"
		}
		if _, err := reader.ReadArrangementHistory(t.Context(), foreign, record.ID); err == nil {
			t.Fatal("foreign history readable")
		}
		if _, err := reader.ArrangementRefs(t.Context(), foreign); err == nil {
			t.Fatal("foreign references readable")
		}
	}
	if _, err := reader.ReadArrangementHistory(t.Context(), f.scope, "unknown"); err == nil {
		t.Fatal("invented record accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.ReadArrangementHistory(ctx, f.scope, record.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	if _, err := reader.ArrangementRefs(ctx, f.scope); !errors.Is(err, context.Canceled) {
		t.Fatal("list cancellation ignored", err)
	}
	if !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("history read mutated canonical library")
	}
}
