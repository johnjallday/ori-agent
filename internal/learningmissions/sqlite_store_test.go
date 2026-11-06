package learningmissions

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func testStore(t *testing.T) (*SQLiteStore, *database.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "learning.db")
	db, err := database.Open(t.Context(), &database.Config{Path: path, BusyTimeout: 5000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('workspace','Practice',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return NewSQLiteStore(db), db, path
}

func testInstance(owner, id string) Instance {
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).UnixMilli()
	return Instance{ID: id, OwnerID: owner, Epoch: 1, Revision: 1, State: Offered,
		Definition: Definition{Structure: DelegateInspect, Version: 1, WorkspaceID: "workspace", Incarnation: "local-install-1",
			CandidateRef: "candidate-1", ScopeDigest: digest([]byte("scope")), Goal: "Draft a short summary", Scope: "Only the text you supply",
			Why: "Practice reviewing an outcome", Steps: []string{"scope", "execute", "review"}, Guidance: GuidanceGuided, ExpiresAt: at + OfferLifetime.Milliseconds()},
		CreatedAt: at, UpdatedAt: at}
}

func seedInstance(t *testing.T, s *SQLiteStore, owner, id string) Owner {
	t.Helper()
	before, err := s.Owner(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Change(t.Context(), owner, before.Revision, before.Epoch, true, func(m *Mutation) error {
		return m.InsertInstance(testInstance(owner, id))
	})
	if err != nil {
		t.Fatal(err)
	}
	return after
}

func TestStoreReadDoesNotEnrollAndReloadKeepsContract(t *testing.T) {
	s, db, path := testStore(t)
	owner, err := s.Owner(t.Context(), "local")
	if err != nil || owner.Revision != 0 || owner.Preferences.Mode != "on_request" {
		t.Fatalf("unenrolled read: %+v %v", owner, err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_owners`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("read wrote enrollment: %d %v", count, err)
	}
	seedInstance(t, s, "local", "mission")
	if _, err := s.Instance(t.Context(), "someone-else", "mission"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign instance: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(t.Context(), &database.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := NewSQLiteStore(reopened).Instance(t.Context(), "local", "mission")
	if err != nil || got.Definition.Goal != "Draft a short summary" || got.State != Offered || got.Revision != 1 {
		t.Fatalf("reopened: %+v %v", got, err)
	}
}

func TestStoreRollbackAndImmutableDefinition(t *testing.T) {
	s, _, _ := testStore(t)
	sentinel := errors.New("injected after insert")
	_, err := s.Change(t.Context(), "local", 0, 0, true, func(m *Mutation) error {
		if err := m.InsertInstance(testInstance("local", "mission")); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	owner, _ := s.Owner(t.Context(), "local")
	if owner.Revision != 0 {
		t.Fatal("rollback retained an owner revision")
	}
	if _, err := s.Instance(t.Context(), "local", "mission"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("rollback retained a child record")
	}
	owner = seedInstance(t, s, "local", "mission")
	_, err = s.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		i, err := m.Instance("mission")
		if err != nil {
			return err
		}
		i.Revision++
		i.Definition.Goal = "Changed without acceptance"
		return m.UpdateInstance(i, 1)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("definition mutated: %v", err)
	}
	got, _ := s.Owner(t.Context(), "local")
	if got.Revision != owner.Revision {
		t.Fatal("failed update advanced revision")
	}
}

func TestStoreOneActiveIncludesBlockedAndOwnerScopedOffers(t *testing.T) {
	s, _, _ := testStore(t)
	owner := seedInstance(t, s, "alice", "first")
	owner, err := s.Change(t.Context(), "alice", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		i, err := m.Instance("first")
		if err != nil {
			return err
		}
		i.State, i.Revision = Active, 2
		return m.UpdateInstance(i, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	owner = seedInstance(t, s, "alice", "second")
	for _, state := range []State{Active, Blocked} {
		if state == Blocked {
			owner, err = s.Change(t.Context(), "alice", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
				i, err := m.Instance("first")
				if err != nil {
					return err
				}
				i.State, i.Revision = Blocked, 3
				return m.UpdateInstance(i, 2)
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err = s.Change(t.Context(), "alice", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
			i, err := m.Instance("second")
			if err != nil {
				return err
			}
			i.State, i.Revision = Active, 2
			return m.UpdateInstance(i, 1)
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("second active alongside %s: %v", state, err)
		}
	}
	seedInstance(t, s, "bob", "second") // same ID in another owner is independent
	_, err = s.Change(t.Context(), "alice", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		return m.InsertInstance(testInstance("alice", "third"))
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second offer: %v", err)
	}
}

func TestStoreCASHasOneWinnerAcrossStoreObjects(t *testing.T) {
	s, db, _ := testStore(t)
	owner := seedInstance(t, s, "local", "mission")
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := NewSQLiteStore(db).Change(context.Background(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
				i, err := m.Instance("mission")
				if err != nil {
					return err
				}
				i.State, i.Revision = Active, 2
				return m.UpdateInstance(i, 1)
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d", winners)
	}
}

func TestStoreVersionsBoundsAndDeletionEpoch(t *testing.T) {
	for name, change := range map[string]func(*Instance){
		"unknown structure": func(i *Instance) { i.Definition.Structure = "invented" },
		"future version":    func(i *Instance) { i.Definition.Version++ },
		"extra step":        func(i *Instance) { i.Definition.Steps = append(i.Definition.Steps, "execute_shell") },
		"long goal":         func(i *Instance) { i.Definition.Goal = strings.Repeat("x", 301) },
		"foreign owner":     func(i *Instance) { i.OwnerID = "foreign" },
		"long input":        func(i *Instance) { i.Progress.Input = strings.Repeat("x", MaxDraftBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			s, _, _ := testStore(t)
			_, err := s.Change(t.Context(), "local", 0, 0, true, func(m *Mutation) error {
				i := testInstance("local", "mission")
				change(&i)
				return m.InsertInstance(i)
			})
			if err == nil {
				t.Fatal("invalid record persisted")
			}
		})
	}
	s, db, _ := testStore(t)
	owner := seedInstance(t, s, "local", "mission")
	if _, err := s.Change(t.Context(), "local", owner.Revision, owner.Epoch+1, false, func(*Mutation) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale epoch: %v", err)
	}
	if _, err := s.Change(t.Context(), "missing", 0, 0, false, func(*Mutation) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("callback resurrected owner: %v", err)
	}
	// A future definition may be shown as history, never executed or rewritten.
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_instances SET definition=json_set(definition,'$.version',99) WHERE owner_id='local'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.Instance(t.Context(), "local", "mission")
	if err != nil || got.Definition.Version != 99 {
		t.Fatalf("future history unreadable: %+v %v", got, err)
	}
	_, err = s.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		got.Revision++
		got.State = Active
		return m.UpdateInstance(got, 1)
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("executed unsupported definition: %v", err)
	}
}

func TestStoreDeduplicatesCreditAndGenerationClaims(t *testing.T) {
	s, _, _ := testStore(t)
	owner := seedInstance(t, s, "local", "mission")
	at := testInstance("local", "mission").CreatedAt
	op := Operation{ID: "generate-1", Kind: "generate", Status: "pending", Epoch: 1, RequestDigest: digest([]byte("request")), CreatedAt: at}
	owner, err := s.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		return m.InsertOperation(op)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		op.ID = "generate-2"
		return m.InsertOperation(op)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second generation claim: %v", err)
	}
	_, err = s.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		e := Evidence{Key: digest([]byte("exact-attempt")), ReviewID: "review", Structure: DelegateInspect, Version: 1, Competency: "evaluate", Guidance: GuidanceUnknown, CreatedAt: at}
		for n := range 2 {
			credited, err := m.Credit(e)
			if err != nil {
				return err
			}
			if credited != (n == 0) {
				t.Errorf("credit %d: %t", n, credited)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
