package pathselection

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreIssuesOpaqueExpiringSelection(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	selected := filepath.Join(t.TempDir(), "Private Project")
	token, err := store.Issue(selected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, selected) || len(token) < 32 {
		t.Fatalf("selection token is not opaque: %q", token)
	}
	if got, err := store.Resolve(token); err != nil || got != selected {
		t.Fatalf("resolve = %q, %v", got, err)
	}
	now = now.Add(DefaultTTL)
	if _, err := store.Resolve(token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired resolve error = %v", err)
	}
}
