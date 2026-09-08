package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/store"
)

type resetHostGuardAgentStore struct {
	store.Store
	called bool
}

func (s *resetHostGuardAgentStore) ListAgents() []string {
	s.called = true
	return nil
}

func TestResetHostBuilderRefusesBeforeConfiguration(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("native lease unsupported")
	}
	for _, mode := range []string{"fenced", "cwd", "data", "receipt", "expected", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("ORI_DATA_DIR", root)
			lease, err := resetstate.Acquire(root)
			requireResetNoError(t, err)
			t.Cleanup(func() { requireResetNoError(t, lease.Close()) })
			switch mode {
			case "fenced":
				requireResetNoError(t, lease.WorkGate().TryFence(t.Context()))
			case "cwd":
				t.Chdir(t.TempDir())
			case "data":
				t.Setenv("ORI_DATA_DIR", t.TempDir())
			case "receipt":
				requireResetNoError(t, lease.Replace(resetstate.OperationRecord, []byte(`{"future":true}`)))
			case "expected":
				lease.ExpectOperation()
			case "uncertain":
				lease.MarkUncertain()
			}
			for range 2 {
				builder, err := NewServerBuilderWithResetLease(lease)
				requireResetNoError(t, err)
				if builder.resetWork != lease.WorkGate() || builder.server.resetWork != lease.WorkGate() || builder.server.resetLease != lease {
					t.Fatal("builder replaced host ownership/admission")
				}
				called := false
				builder.configurationPhase = func() error { called = true; return errors.New("fixture prevents all native/provider construction") }
				if _, err := builder.Build(); err == nil || called {
					t.Fatalf("configuration crossed a blocked host: %v", err)
				}
				if builder.resetPreviewOwners().CheckLifecycle == nil {
					t.Fatal("owned host omitted the verified lifecycle capability")
				}
				guard := &resetHostGuardAgentStore{}
				builder.server.Storage.AgentStore = guard
				builder.server.Start()
				if guard.called || !lease.WorkGate().Snapshot().Fenced {
					t.Fatal("blocked background startup touched owners or left HTTP admission open")
				}
				if mode == "fenced" && lease.Uncertain() {
					t.Fatal("an ordinary late Start turned an existing fence into uncertain admission")
				}
			}
			if got := lease.WorkGate().Snapshot().Active; got != 0 {
				t.Fatalf("refused builders retained %d permits", got)
			}
			for _, name := range []string{"settings.json", "app_state.json", "sessions.db", "agents.json"} {
				if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatal("application store opened:", name, err)
				}
			}
		})
	}
}

func TestResetHostFailedBuilderProcessHelper(t *testing.T) {
	if os.Getenv("ORI_RESET_BUILDER_CHILD") != "1" {
		return
	}
	lease, err := resetstate.BeforeStores(os.Getenv("ORI_DATA_DIR"))
	requireResetNoError(t, err)
	b, err := NewServerBuilderWithResetLease(lease)
	requireResetNoError(t, err)
	calls := 0
	failure := errors.New("owned constructor failure")
	b.configurationPhase = func() error {
		calls++
		if err := lease.WorkGate().TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
			t.Fatal("configuration not tracked:", err)
		}
		return failure // Never instantiate credentials, providers or broad stores.
	}
	if _, err := b.Build(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if !lease.Uncertain() || !lease.WorkGate().Snapshot().Fenced || lease.WorkGate().Snapshot().Active != 0 {
		t.Fatal("failed build became a freely restartable boundary")
	}
	if _, err := b.Build(); !errors.Is(err, resetstate.ErrWorkFenced) || calls != 1 {
		t.Fatal("failed build retried construction:", err)
	}
	if err := lease.Close(); !errors.Is(err, resetstate.ErrPinned) {
		t.Fatal("failed build released ownership:", err)
	}
}

func TestResetHostFailedBuilderStaysFencedUntilProcessExit(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("native lease unsupported")
	}
	root := t.TempDir()
	executable, err := os.Executable()
	requireResetNoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// #nosec G204 -- only this test binary with an owned root and constructor refusal seam.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestResetHostFailedBuilderProcessHelper$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "ORI_DATA_DIR=" + root, "ORI_RESET_BUILDER_CHILD=1"}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned builder: %v\n%s", err, output)
	}
	lease, err := resetstate.Acquire(root)
	requireResetNoError(t, err)
	requireResetNoError(t, lease.Close())
}
