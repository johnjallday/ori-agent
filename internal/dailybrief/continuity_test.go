package dailybrief

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func briefContinuityMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func briefContinuityWorkspace(t *testing.T, store *SQLiteStore, id string) {
	t.Helper()
	_, err := store.db.ExecContext(t.Context(), `INSERT INTO users(id,created_at,updated_at) VALUES ('local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) ON CONFLICT DO NOTHING`)
	briefContinuityMust(t, err)
	_, err = store.db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?, 'Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id)
	briefContinuityMust(t, err)
}

func briefContinuitySource(t *testing.T, requested Scope) (*SQLiteStore, workspacecontinuity.Record, *Config) {
	t.Helper()
	source := newTestStore(t)
	briefContinuityWorkspace(t, source, "hq")
	cfg := &Config{WorkspaceID: "hq", UserID: "local", Timezone: "America/New_York", ScheduleDays: []string{"wed", "fri"},
		ScheduleTime: "09:25", ScheduleEnabled: true, NotifyOnReady: true, Scope: requested,
		SelectedWorkspaceIDs: []string{"hq", "outside-source"}, IncludeFutureWorkspaces: true}
	briefContinuityMust(t, source.UpsertConfig(t.Context(), cfg))
	briefContinuityMust(t, source.UpsertConfig(t.Context(), cfg))
	saved, err := source.GetConfig(t.Context(), "hq")
	briefContinuityMust(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var record *workspacecontinuity.Record
	briefContinuityMust(t, source.db.InTransaction(ctx, func(tx *sql.Tx) error {
		record, err = source.SnapshotContinuityConfig(ctx, tx, "hq")
		return err
	}))
	if record == nil {
		t.Fatal("saved config absent from snapshot")
	}
	return source, *record, saved
}

func briefContinuityDestination(t *testing.T) (*SQLiteStore, workspacecontinuity.RestoreScope) {
	t.Helper()
	destination := newTestStore(t)
	briefContinuityWorkspace(t, destination, "unrelated-destination")
	local := workspacecontinuity.NewLocalStore(destination.db)
	op := workspacecontinuity.Operation{ID: uuid.NewString(), UserID: "local", Action: workspacecontinuity.WorkspaceOnly,
		TreeDigest: workspacecontinuity.Digest([]byte("tree")), DestinationDigest: workspacecontinuity.Digest([]byte("destination"))}
	var members []workspacecontinuity.ImportMember
	for _, id := range []string{"hq", "child"} {
		members = append(members, workspacecontinuity.ImportMember{WorkspaceID: id, Generation: uuid.NewString(),
			Digest: workspacecontinuity.Digest([]byte(id)), Disposition: workspacecontinuity.Ordinary})
	}
	_, err := local.BeginImport(t.Context(), op, members)
	briefContinuityMust(t, err)
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: "hq", UserID: "local"}
	for _, member := range members {
		briefContinuityMust(t, destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
			memberScope := scope
			memberScope.WorkspaceID = member.WorkspaceID
			_, err := workspacecontinuity.ClaimRecord(t.Context(), tx, memberScope, "workspace", "workspaces", member.WorkspaceID, workspacecontinuity.Digest([]byte(member.WorkspaceID)))
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?,'Imported',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, member.WorkspaceID)
			return err
		}))
	}
	return destination, scope
}

func TestContinuityConfigPreservesDatesAndBoundsIntent(t *testing.T) {
	for _, requested := range []Scope{ScopeAll, ScopeSelected} {
		t.Run(string(requested), func(t *testing.T) {
			_, record, sourceConfig := briefContinuitySource(t, requested)
			destination, scope := briefContinuityDestination(t)
			briefContinuityMust(t, destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
				created, err := destination.RestoreContinuityConfig(t.Context(), tx, scope, record)
				if err == nil && !created {
					t.Error("first restore did not insert")
				}
				return err
			}))
			got, err := destination.GetConfig(t.Context(), "hq")
			briefContinuityMust(t, err)
			if got.ConfigRevision != 2 || !got.CreatedAt.Equal(sourceConfig.CreatedAt) || !got.UpdatedAt.Equal(sourceConfig.UpdatedAt) ||
				got.Timezone != sourceConfig.Timezone || got.ScheduleTime != sourceConfig.ScheduleTime || !reflect.DeepEqual(got.ScheduleDays, sourceConfig.ScheduleDays) {
				t.Fatal("config source choices/dates/revision were changed")
			}
			wantScope := []string{"child", "hq"}
			if requested == ScopeSelected {
				wantScope = []string{"hq"}
			}
			if got.ScheduleEnabled || got.NotifyOnReady || got.IncludeFutureWorkspaces || got.Scope != ScopeSelected || !reflect.DeepEqual(got.SelectedWorkspaceIDs, wantScope) {
				t.Fatalf("import expanded access or restored execution intent as authority: %+v", got)
			}
			intent, err := DecodeContinuityConfig(record)
			briefContinuityMust(t, err)
			if !intent.ScheduleEnabled || !intent.NotifyOnReady || !intent.IncludeFutureWorkspaces || intent.Scope != requested {
				t.Fatal("source intent was lost")
			}
			got.ScheduleTime, got.ScheduleEnabled = "15:40", true
			briefContinuityMust(t, destination.UpsertConfig(t.Context(), got))
			for _, deleted := range []bool{false, true} {
				if deleted {
					_, err = destination.db.ExecContext(t.Context(), `DELETE FROM daily_brief_config WHERE workspace_id='hq'`)
					briefContinuityMust(t, err)
				}
				briefContinuityMust(t, destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
					created, err := destination.RestoreContinuityConfig(t.Context(), tx, scope, record)
					if created {
						t.Fatal("retry overwrote an edit or resurrected a deletion")
					}
					return err
				}))
				current, err := destination.GetConfig(t.Context(), "hq")
				if deleted {
					if !errors.Is(err, ErrConfigNotFound) {
						t.Fatalf("deleted config returned: %v", err)
					}
				} else if err != nil || current.ScheduleTime != "15:40" || !current.ScheduleEnabled || current.ConfigRevision != 3 {
					t.Fatal("local config edit lost on retry")
				}
			}
		})
	}
}

func TestContinuityConfigPreservesFrozenAllCutoff(t *testing.T) {
	_, record, _ := briefContinuitySource(t, ScopeAll)
	c, err := DecodeContinuityConfig(record)
	briefContinuityMust(t, err)
	c.IncludeFutureWorkspaces = false
	record, err = workspacecontinuity.EncodeRecord(record.ID, c)
	briefContinuityMust(t, err)
	destination, scope := briefContinuityDestination(t)
	_, err = destination.db.ExecContext(t.Context(), `UPDATE workspaces SET created_at=? WHERE id='hq'`, c.UpdatedAt.Add(-time.Hour))
	briefContinuityMust(t, err)
	_, err = destination.db.ExecContext(t.Context(), `UPDATE workspaces SET created_at=? WHERE id='child'`, c.UpdatedAt.Add(time.Hour))
	briefContinuityMust(t, err)
	briefContinuityMust(t, destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := destination.RestoreContinuityConfig(t.Context(), tx, scope, record)
		return err
	}))
	got, err := destination.GetConfig(t.Context(), "hq")
	briefContinuityMust(t, err)
	if !reflect.DeepEqual(got.SelectedWorkspaceIDs, []string{"hq"}) {
		t.Fatal("frozen source scope expanded to a newer child")
	}
}

func TestContinuityConfigRefusesCollisionAndRollsBackClaim(t *testing.T) {
	_, record, _ := briefContinuitySource(t, ScopeAll)
	for _, collision := range []string{"config", "workspace-ownership"} {
		t.Run(collision, func(t *testing.T) {
			destination, scope := briefContinuityDestination(t)
			if collision == "config" {
				briefContinuityMust(t, destination.UpsertConfig(t.Context(), &Config{WorkspaceID: "hq", UserID: "local", Timezone: "UTC", ScheduleTime: "17:00"}))
			} else {
				_, err := destination.db.ExecContext(t.Context(), `DELETE FROM continuity_records WHERE domain='workspace' AND record_id='hq'`)
				briefContinuityMust(t, err)
			}
			err := destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
				_, err := destination.RestoreContinuityConfig(t.Context(), tx, scope, record)
				return err
			})
			if !errors.Is(err, workspacecontinuity.ErrConflict) {
				t.Fatalf("collision accepted: %v", err)
			}
			var claims int
			briefContinuityMust(t, destination.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM continuity_records WHERE domain='brief_config'`).Scan(&claims))
			if claims != 0 {
				t.Fatal("failed insert leaked record claim")
			}
			if collision == "config" {
				got, err := destination.GetConfig(t.Context(), "hq")
				briefContinuityMust(t, err)
				if got.ScheduleTime != "17:00" {
					t.Fatal("incumbent changed")
				}
			}
		})
	}
}

func TestContinuityConfigRejectsMalformedDataWithoutDefaults(t *testing.T) {
	source, record, _ := briefContinuitySource(t, ScopeAll)
	for name, mutate := range map[string]func(*ContinuityConfig){
		"version":        func(c *ContinuityConfig) { c.Version++ },
		"owner":          func(c *ContinuityConfig) { c.SourceUserID = "foreign" },
		"timezone":       func(c *ContinuityConfig) { c.Timezone = "" },
		"local-timezone": func(c *ContinuityConfig) { c.Timezone = "Local" },
		"schedule":       func(c *ContinuityConfig) { c.ScheduleTime = "8:00" },
		"empty-days":     func(c *ContinuityConfig) { c.ScheduleDays = nil },
		"bad-days":       func(c *ContinuityConfig) { c.ScheduleDays = []string{"bad"} },
		"duplicate-days": func(c *ContinuityConfig) { c.ScheduleDays = []string{"mon", "mon"} },
		"unknown-scope":  func(c *ContinuityConfig) { c.Scope = "everything" },
		"duplicate-ids":  func(c *ContinuityConfig) { c.SelectedWorkspaceIDs = []string{"hq", "hq"} },
		"date":           func(c *ContinuityConfig) { c.CreatedAt = time.Time{} },
		"revision":       func(c *ContinuityConfig) { c.ConfigRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c, err := DecodeContinuityConfig(record)
			briefContinuityMust(t, err)
			mutate(&c)
			bad, err := workspacecontinuity.EncodeRecord(record.ID, c)
			briefContinuityMust(t, err)
			if _, err := DecodeContinuityConfig(bad); err == nil {
				t.Fatal("malformed configuration normalized into recovered settings")
			}
		})
	}
	for _, replacement := range []string{`"scope":"all","Scope":"selected"`, `"scope":"all","api_key":"synthetic-secret"`} {
		bad := record
		bad.Data = []byte(strings.Replace(string(record.Data), `"scope":"all"`, replacement, 1))
		if _, err := DecodeContinuityConfig(bad); !errors.Is(err, workspacecontinuity.ErrInvalid) {
			t.Fatalf("unknown/ambiguous field accepted: %v", err)
		}
	}
	if got, err := source.SnapshotContinuityConfig(t.Context(), source.db, "absent"); err != nil || got != nil {
		t.Fatal("absent config became default settings")
	}
	_, err := source.db.ExecContext(t.Context(), `UPDATE daily_brief_config SET schedule_days='broken' WHERE workspace_id='hq'`)
	briefContinuityMust(t, err)
	if _, err := source.SnapshotContinuityConfig(t.Context(), source.db, "hq"); !errors.Is(err, workspacecontinuity.ErrInvalid) {
		t.Fatalf("bad source configuration silently exported: %v", err)
	}
}
