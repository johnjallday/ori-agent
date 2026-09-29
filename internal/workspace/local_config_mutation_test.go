package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestLocalConfigFailedCleanupRemainsFencedUntilFullReplacement(t *testing.T) {
	local, files, ws, folder := localConfigFixture(t)
	localConfigMust(t, files.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()))
	localConfigMust(t, local.MigrateAgentFile(t.Context(), folder, ws.ID, "Guide"))
	profile, err := local.ReadAgentFile(t.Context(), folder, ws.ID, "Guide")
	localConfigMust(t, err)
	_, err = local.db.ExecContext(t.Context(), `CREATE TRIGGER fail_private_cleanup BEFORE DELETE ON workspace_local_config
		BEGIN SELECT RAISE(FAIL,'synthetic cleanup failure'); END`)
	localConfigMust(t, err)
	profile.Settings.Model = "edited-offline-model"
	if err := local.WriteAgentFile(t.Context(), folder, ws.ID, "Guide", profile); err == nil {
		t.Fatal("cleanup failure hidden after file publication")
	}
	coordination := workspacecontinuity.NewLocalStore(local.db)
	state, err := coordination.Preparation(t.Context(), ws.ID)
	localConfigMust(t, err)
	if !state.PendingMutation || !state.MutationFailed || state.Ready() {
		t.Fatal("failed cleanup not fenced")
	}
	_, err = local.db.ExecContext(t.Context(), `DROP TRIGGER fail_private_cleanup`)
	localConfigMust(t, err)
	profile, err = local.ReadAgentFile(t.Context(), folder, ws.ID, "Guide")
	localConfigMust(t, err)
	if profile.Settings.Model != "edited-offline-model" || profile.Settings.APIKey != "synthetic-agent-secret" {
		t.Fatal("durable native edit was lost because preparation bookkeeping failed")
	}
	localConfigMust(t, local.WriteAgentFile(t.Context(), folder, ws.ID, "Guide", profile))
	state, err = coordination.Preparation(t.Context(), ws.ID)
	localConfigMust(t, err)
	if state.PendingMutation || state.Ready() {
		t.Fatal("replacement did not clear only the barrier and leave preparation dirty")
	}
}

func TestLocalConfigMutationCompletionOutlivesRequestCancellation(t *testing.T) {
	local, _, ws, _ := localConfigFixture(t)
	coordination := workspacecontinuity.NewLocalStore(local.db)
	mutation, err := coordination.BeginFileMutation(t.Context(), ws.ID, "workspace.json")
	localConfigMust(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := local.finishFileMutation(ctx, mutation, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	state, err := coordination.Preparation(t.Context(), ws.ID)
	localConfigMust(t, err)
	if !state.MutationFailed {
		t.Fatal("cancellation skipped completion bookkeeping")
	}
}

func TestLocalConfigResetFencePrecedesCanonicalMutationCallbacks(t *testing.T) {
	local, original, ws, folder := localConfigFixture(t)
	gate := &resetstate.WorkGate{}
	tracked, err := NewLocalConfigStoreWithWorkGate(local.db, local.secrets, gate)
	localConfigMust(t, err)
	files, err := NewFileStoreWithLocalConfig(original.BasePath(), tracked)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	if _, _, err := files.Import(folder); !errors.Is(err, ErrReviewedWorkspaceImportRequired) {
		t.Fatal("private composition exposed legacy unreviewed import", err)
	}
	localConfigMust(t, gate.TryFence(t.Context()))
	mutate := func(*Workspace) error { t.Fatal("fenced mutation callback ran"); return nil }
	wrapped := NewAgentSnapshotStore(files, nil)
	for name, call := range map[string]func() error{
		"save":           func() error { return files.Save(ws) },
		"save-at":        func() error { return files.SaveAt(ws, t.TempDir()) },
		"rebind":         func() error { return files.RebindExistingFolder(ws, folder) },
		"update":         func() error { return files.Update(ws.ID, mutate) },
		"snapshots":      func() error { return wrapped.Update(ws.ID, mutate) },
		"rename":         func() error { return files.Rename(ws.ID, "Changed") },
		"move":           func() error { _, err := files.MoveWorkspaceFolder(ws.ID, ""); return err },
		"delete":         func() error { return files.Delete(ws.ID) },
		"trash":          func() error { _, _, err := files.Trash(ws.ID); return err },
		"root":           func() error { _, err := files.SetBasePath(t.TempDir()); return err },
		"import":         func() error { _, _, err := files.Import(folder); return err },
		"snapshot-write": func() error { return files.SaveWorkspaceAgent(ws.ID, "Guide", syntheticPrivateAgent()) },
		"prepare":        func() error { return files.PrepareNativeLocalConfig(t.Context(), ws.ID) },
	} {
		if err := call(); !errors.Is(err, resetstate.ErrWorkFenced) {
			t.Fatalf("%s bypassed reset fence: %v", name, err)
		}
	}
	newRoot := filepath.Join(t.TempDir(), "must-not-exist")
	if _, err := NewFileStoreWithLocalConfig(newRoot, tracked); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if _, err := os.Stat(newRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fenced constructor created a root")
	}
	if _, err := NewLocalConfigStoreWithWorkGate(local.db, local.secrets, nil); !errors.Is(err, resetstate.ErrWorkUntracked) {
		t.Fatal("nil gate claimed tracking")
	}
}

type blockedContinuityPrimary struct {
	Store
	entered, proceed chan struct{}
	failure          error
	once             sync.Once
}

func (s *blockedContinuityPrimary) Save(ws *Workspace) error {
	s.once.Do(func() { close(s.entered) })
	<-s.proceed
	if s.failure != nil {
		return s.failure
	}
	return s.Store.Save(ws)
}

func TestLocalConfigSyncSaveHoldsBarrierAndResetPermitThroughPrimaryAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "primary_failure"}[fail], func(t *testing.T) {
			local, original, ws, folder := localConfigFixture(t)
			gate := &resetstate.WorkGate{}
			tracked, err := NewLocalConfigStoreWithWorkGate(local.db, local.secrets, gate)
			localConfigMust(t, err)
			files, err := NewFileStoreWithLocalConfig(original.BasePath(), tracked)
			localConfigMust(t, err)
			t.Cleanup(func() { _ = files.Close() })
			primary := NewInMemoryStore()
			localConfigMust(t, primary.Save(ws))
			blocked := &blockedContinuityPrimary{Store: primary, entered: make(chan struct{}), proceed: make(chan struct{})}
			if fail {
				blocked.failure = errors.New("synthetic primary failure")
			}
			synced := NewSyncStore(blocked, files)
			edit, err := cloneWorkspaceForRebind(ws)
			localConfigMust(t, err)
			edit.Description = "Saved while preparation waits"
			done := make(chan error, 1)
			var resume sync.Once
			unblock := func() { resume.Do(func() { close(blocked.proceed) }) }
			completed := false
			defer func() {
				unblock()
				if !completed {
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("save did not drain")
					}
				}
			}()
			go func() { done <- synced.Save(edit) }()
			select {
			case <-blocked.entered:
			case err := <-done:
				completed = true
				t.Fatalf("save never reached primary: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("save did not reach primary")
			}
			coordination := workspacecontinuity.NewLocalStore(local.db)
			state, err := coordination.Preparation(t.Context(), ws.ID)
			localConfigMust(t, err)
			if !state.PendingMutation || state.Ready() {
				t.Fatal("inner file completion exposed incomplete primary write")
			}
			if _, err := coordination.ReadSnapshot(t.Context(), ws.ID, func(workspacecontinuity.Queryer, uint64) error { return nil }); !errors.Is(err, workspacecontinuity.ErrMutationPending) {
				t.Fatal(err)
			}
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatal("reset crossed active primary write", err)
			}
			unblock()
			err = <-done
			completed = true
			if fail && !errors.Is(err, blocked.failure) || !fail && err != nil {
				t.Fatal("unexpected save result", err)
			}
			state, err = coordination.Preparation(t.Context(), ws.ID)
			localConfigMust(t, err)
			if state.MutationFailed != fail || state.PendingMutation != fail || state.Ready() {
				t.Fatal("wrong completed/failed state")
			}
			if gate.Snapshot().Active != 0 {
				t.Fatal("write/rollback leaked a work permit")
			}
			localConfigMust(t, gate.TryFence(t.Context()))
			before, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
			localConfigMust(t, err)
			if err := synced.Save(edit); !errors.Is(err, resetstate.ErrWorkFenced) {
				t.Fatal("save bypassed reset fence", err)
			}
			if _, err := tracked.Stage(t.Context(), ws.ID, "agent", "guide", []byte(`{}`)); !errors.Is(err, resetstate.ErrWorkFenced) {
				t.Fatal("private staging bypassed fence", err)
			}
			after, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
			localConfigMust(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("fenced save changed retained folder")
			}
		})
	}
}
