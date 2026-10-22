package personalassistant

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newHireRecordsForTest(t *testing.T) *SQLiteHireRecords {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "hires.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteHireRecords(db)
}

func sampleHire(id, requestID, workspaceID string) HireRecord {
	return HireRecord{
		ID: id, UserID: "local", RequestID: requestID, AgentName: "Rita", WorkspaceID: workspaceID,
		Brief: "Check the work here.", Role: string(types.RoleValidator), Blueprint: HireBlueprintBlank,
		State: HireStateLooking,
	}
}

func TestHireRecords_CreateGetAndFindByRequestAndWorkspace(t *testing.T) {
	records := newHireRecordsForTest(t)
	created, err := records.Create(t.Context(), sampleHire("hire-1", "req-1", "ws-1"))
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() || !created.FinishedAt.IsZero() {
		t.Fatalf("timestamps %+v", created)
	}
	if _, err := records.Create(t.Context(), sampleHire("hire-2", "req-2", "ws-1")); err != nil {
		t.Fatal(err)
	}

	got, err := records.Get(t.Context(), "hire-1")
	if err != nil || got.RequestID != "req-1" || got.State != HireStateLooking || got.Brief != "Check the work here." {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	byRequest, err := records.ByRequestID(t.Context(), "req-2")
	if err != nil || byRequest.ID != "hire-2" {
		t.Fatalf("ByRequestID = %+v, %v", byRequest, err)
	}
	byWorkspace, err := records.ByWorkspace(t.Context(), "ws-1")
	if err != nil || len(byWorkspace) != 2 {
		t.Fatalf("ByWorkspace = %d, %v", len(byWorkspace), err)
	}
	mine, err := records.List(t.Context(), "local")
	if err != nil || len(mine) != 2 {
		t.Fatalf("List = %d, %v", len(mine), err)
	}
	if others, _ := records.List(t.Context(), "someone-else"); len(others) != 0 {
		t.Fatalf("another user sees %d hires", len(others))
	}
	if _, err := records.Get(t.Context(), "missing"); !errors.Is(err, ErrHireNotFound) {
		t.Fatalf("missing hire: %v", err)
	}
}

func TestHireRecords_RequestIDIsUsedOnce(t *testing.T) {
	records := newHireRecordsForTest(t)
	if _, err := records.Create(t.Context(), sampleHire("hire-1", "req-1", "ws-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Create(t.Context(), sampleHire("hire-2", "req-1", "ws-2")); !errors.Is(err, ErrHireRequestUsed) {
		t.Fatalf("a reused request id: %v", err)
	}
}

func TestHireRecords_UpdateChangesProgressNotIdentity(t *testing.T) {
	records := newHireRecordsForTest(t)
	if _, err := records.Create(t.Context(), sampleHire("hire-1", "req-1", "ws-1")); err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	updated, err := records.Update(t.Context(), "hire-1", func(r *HireRecord) error {
		r.RunID, r.State, r.FinishedAt = "run-1", HireStateReported, finished
		r.AgentName, r.WorkspaceID, r.Brief = "Mallory", "ws-other", "Something else."
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := records.Get(t.Context(), "hire-1")
	for _, record := range []HireRecord{updated, got} {
		if record.RunID != "run-1" || record.State != HireStateReported || !record.FinishedAt.Equal(finished) {
			t.Fatalf("progress not kept: %+v", record)
		}
		if record.AgentName != "Rita" || record.WorkspaceID != "ws-1" || record.Brief != "Check the work here." {
			t.Fatalf("identity changed: %+v", record)
		}
	}

	refused := errors.New("refused")
	if _, err := records.Update(t.Context(), "hire-1", func(*HireRecord) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("a refused mutation: %v", err)
	}
	if _, err := records.Update(t.Context(), "hire-1", func(r *HireRecord) error { r.State = "hired"; return nil }); !errors.Is(err, ErrValidation) {
		t.Fatalf("an unknown state: %v", err)
	}
	if _, err := records.Update(t.Context(), "missing", func(*HireRecord) error { return nil }); !errors.Is(err, ErrHireNotFound) {
		t.Fatalf("a missing hire: %v", err)
	}
}

func TestHireRecords_RefuseUnboundedOrPathLikeText(t *testing.T) {
	records := newHireRecordsForTest(t)
	for name, mutate := range map[string]func(*HireRecord){
		"path agent name":  func(r *HireRecord) { r.AgentName = "/Users/me/Agents/Rita" },
		"empty workspace":  func(r *HireRecord) { r.WorkspaceID = "" },
		"long request id":  func(r *HireRecord) { r.RequestID = strings.Repeat("r", 129) },
		"long brief":       func(r *HireRecord) { r.Brief = strings.Repeat("b", WorkspaceGoalMaxRunes+1) },
		"control in brief": func(r *HireRecord) { r.Brief = "check\x00" },
		"unknown state":    func(r *HireRecord) { r.State = "hired" },
	} {
		t.Run(name, func(t *testing.T) {
			record := sampleHire("hire-"+name, "req-"+name, "ws-1")
			mutate(&record)
			if _, err := records.Create(t.Context(), record); !errors.Is(err, ErrValidation) {
				t.Fatalf("stored: %v", err)
			}
		})
	}
}
