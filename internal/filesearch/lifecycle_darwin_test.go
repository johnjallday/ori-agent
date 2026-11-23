//go:build darwin

package filesearch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInterruptedScanDoesNotRestartAndRevokedRefreshCannotCommit(t *testing.T) {
	f := setup(t)
	path := filepath.Join(f.docs, "Harbor.txt")
	must(t, os.WriteFile(path, []byte("synthetic"), 0600))
	pause := func() <-chan struct{} {
		started := make(chan struct{})
		f.afterPublish = func(ctx context.Context) { close(started); <-ctx.Done() }
		return started
	}
	started := pause()
	v, e := f.choose(t, "documents")
	must(t, e)
	wait := func(c <-chan struct{}) {
		select {
		case <-c:
		case <-time.After(3 * time.Second):
			t.Fatal("never reached batch barrier")
		}
	}
	wait(started)
	f.Close()
	f.Service, e = New(t.Context(), f.Options)
	must(t, e)
	page, e := f.Query(t.Context(), "local", "Harbor", "", 1)
	must(t, e)
	if page.Sources[0].State != "interrupted" || len(page.Hits) != 1 || f.gate.Snapshot().Active != 0 {
		t.Fatal("interrupted coverage lost or work restarted")
	}
	started = pause()
	must(t, f.Refresh(t.Context(), "local", v.ID, 1))
	wait(started)
	if f.Act(t.Context(), "local", page.Hits[0].ID, 1, true) != ErrMissing {
		t.Fatal("refresh retained obsolete action identity")
	}
	must(t, f.Remove(t.Context(), "local", v.ID))
	f.wg.Wait()
	f.Close()
	f.Service, e = New(t.Context(), f.Options)
	must(t, e)
	page, e = f.Query(t.Context(), "local", "", "", 1)
	must(t, e)
	if len(page.Hits) != 0 || len(page.Sources) != 0 {
		t.Fatal("revoked generation returned after restart")
	}
	data, e := os.ReadFile(path)
	must(t, e)
	if string(data) != "synthetic" {
		t.Fatal("source bytes changed")
	}
}

func TestLatePickerCannotUndoRemoval(t *testing.T) {
	f := setup(t)
	v, e := f.choose(t, "documents")
	must(t, e)
	f.await(t, "ready")
	started, release := make(chan struct{}), make(chan struct{})
	f.Pick = func(ctx context.Context) (string, bool, error) {
		close(started)
		select {
		case <-release:
			return f.docs, true, nil
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	intent, e := f.Begin("local", "custom")
	must(t, e)
	done := make(chan error, 1)
	go func() { _, e := f.Select(t.Context(), "local", intent); done <- e }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("picker not entered")
	}
	must(t, f.Remove(t.Context(), "local", v.ID))
	close(release)
	if <-done != ErrChoice {
		t.Fatal("old picker committed after removal")
	}
	ss, e := f.Sources(t.Context(), "local")
	must(t, e)
	if len(ss) != 0 {
		t.Fatal("old picker regranted source")
	}
}
