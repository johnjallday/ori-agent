package resetstate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRuntimeEntryRefusesChangedRootsAndRecoveryBeforePinning(t *testing.T) {
	for _, mode := range []string{"cwd", "data", "missing", "receipt", "expected", "closed"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			lease := acquireTestLease(t, root)
			cwd, data, want := root, root, ErrRuntimeRootMismatch
			switch mode {
			case "cwd":
				cwd = t.TempDir()
			case "data":
				data = t.TempDir()
			case "missing":
				data = ""
			case "receipt":
				if err := lease.Replace(OperationRecord, []byte(`{"unsupported":true}`)); err != nil {
					t.Fatal(err)
				}
				want = ErrRecoveryRequired
			case "expected":
				lease.ExpectOperation()
				want = ErrRecoveryRequired
			case "closed":
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
				want = ErrClosed
			}
			release, err := lease.EnterRuntime(cwd, data)
			if release != nil || !errors.Is(err, want) {
				t.Fatalf("entry=%v, want %v", err, want)
			}
			if lease.WorkGate().Snapshot().Active != 0 {
				t.Fatal("refused entry leaked a permit")
			}
			if err := lease.Close(); err != nil {
				t.Fatalf("refusal pinned before construction: %v", err)
			}
		})
	}
}

func TestRuntimeEntryAcceptsOnlyExplicitlyVerifiedRecovery(t *testing.T) {
	if !Supported() {
		t.Skip("native lease unsupported")
	}
	root := t.TempDir()
	lease, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.AuthorizeRecoveredRuntime(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("runtime was authorized without a receipt:", err)
	}
	if err := lease.Replace(OperationRecord, []byte(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	lease.ExpectOperation()
	if _, err := lease.EnterRuntime(root, root); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("unverified receipt entered runtime:", err)
	}
	if err := lease.AuthorizeRecoveredRuntime(); err != nil {
		t.Fatal(err)
	}
	release, err := lease.EnterRuntime(root, root)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := lease.Close(); !errors.Is(err, ErrPinned) {
		t.Fatal("recovered runtime did not retain process ownership:", err)
	}
}

func TestRuntimeUncertaintyFencesNewWorkWithoutCancellingActiveWork(t *testing.T) {
	lease := acquireTestLease(t, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	release, err := lease.WorkGate().Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	lease.MarkUncertain()
	if !lease.Uncertain() || !lease.WorkGate().Snapshot().Fenced {
		t.Fatal("uncertainty did not reach host gate")
	}
	if ctx.Err() != nil {
		t.Fatal("uncertainty cancelled admitted work")
	}
	if _, err := lease.WorkGate().Enter(); !errors.Is(err, ErrWorkFenced) {
		t.Fatal(err)
	}
	if err := lease.WorkGate().TryFence(t.Context()); !errors.Is(err, ErrWorkActive) {
		t.Fatalf("active work disappeared: %v", err)
	}
	release()
	if _, err := lease.EnterRuntime(lease.Path(), lease.Path()); !errors.Is(err, ErrWorkFenced) {
		t.Fatal(err)
	}
}

func TestRuntimeLeaseProcessHelper(t *testing.T) {
	if os.Getenv("ORI_RESET_RUNTIME_CHILD") != "1" {
		return
	}
	root := os.Getenv("ORI_DATA_DIR")
	lease, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	gate := lease.WorkGate()
	alias := filepath.Join(root, "runtime-alias")
	if err := os.Symlink(".", alias); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{root, alias} {
		release, err := lease.EnterRuntime(cwd, root)
		if err != nil {
			t.Fatal(err)
		}
		if lease.WorkGate() != gate {
			t.Fatal("runtime replaced host admission")
		}
		if err := gate.TryFence(t.Context()); !errors.Is(err, ErrWorkActive) {
			t.Fatalf("untracked construction: %v", err)
		}
		release()
		if err := lease.Close(); !errors.Is(err, ErrPinned) {
			t.Fatal("construction released ownership:", err)
		}
	}
	runtime.GC()
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.EnterRuntime(root, root); !errors.Is(err, ErrWorkFenced) {
		t.Fatal("runtime bypassed fence:", err)
	}
	if duplicate, err := Acquire(root); duplicate != nil || !errors.Is(err, ErrInUse) {
		t.Fatal("ownership lost before exit:", err)
	}
}

func TestRuntimeGateOnlyResetsOnOwnedProcessExit(t *testing.T) {
	if !Supported() {
		t.Skip("native lease unsupported")
	}
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// #nosec G204 -- only this test executable and a fixed helper; no app stores or credentials.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeLeaseProcessHelper$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "ORI_DATA_DIR=" + root, "ORI_RESET_RUNTIME_CHILD=1"}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned host: %v\n%s", err, output)
	}
	lease := acquireTestLease(t, root)
	if got := lease.WorkGate().Snapshot(); !got.Known || got.Fenced || got.Active != 0 {
		t.Fatalf("new process gate: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, Directory, lockName)); err != nil {
		t.Fatal(err)
	}
}
