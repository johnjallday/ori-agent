package filejanitor

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Two workspaces sending to each other at the same moment take the same two
// folder locks in opposite argument order. lockRoots must still take them in one
// fixed order, or each could hold one lock and wait forever for the other.
func TestLockRoots_OppositeDirectionsNeverDeadlock(t *testing.T) {
	base := t.TempDir()
	downloads := filepath.Join(base, "Downloads")
	documents := filepath.Join(base, "Documents")

	var wg sync.WaitGroup
	counter := 0
	work := func(first, second string) {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			unlock := lockRoots(first, second)
			counter++ // guarded by both locks; -race reports any unguarded access
			unlock()
		}
	}
	wg.Add(4)
	go work(downloads, documents)
	go work(documents, downloads)
	go work(downloads, documents)
	go work(documents, downloads)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("two opposite-direction sends deadlocked")
	}
	if counter != 2000 {
		t.Fatalf("counter = %d, want 2000", counter)
	}
}

// A send and an ordinary one-folder operation on either folder exclude each other.
func TestLockRoots_ExcludesEachFoldersOwnLock(t *testing.T) {
	base := t.TempDir()
	downloads := filepath.Join(base, "Downloads")
	documents := filepath.Join(base, "Documents")

	for _, single := range []string{downloads, documents} {
		unlock := lockRoots(downloads, documents)
		acquired := make(chan struct{})
		go func() {
			release := lockRoot(single)
			close(acquired)
			release()
		}()
		select {
		case <-acquired:
			t.Fatalf("lockRoot(%s) went ahead while a send held both folders", filepath.Base(single))
		case <-time.After(50 * time.Millisecond):
		}
		unlock()
		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatalf("lockRoot(%s) never went ahead after the send released", filepath.Base(single))
		}
	}
}

// A folder named twice, or under two spellings of one key, is locked once:
// locking it twice would deadlock the caller on its own lock.
func TestLockRoots_AFolderNamedTwiceIsLockedOnce(t *testing.T) {
	base := t.TempDir()
	downloads := filepath.Join(base, "Downloads")
	done := make(chan struct{})
	go func() {
		unlock := lockRoots(downloads, downloads+"/", filepath.Join(base, "downloads"))
		unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a folder named twice deadlocked on its own lock")
	}
	// And it is released: a later single lock goes ahead at once.
	release := lockRoot(downloads)
	release()
}
