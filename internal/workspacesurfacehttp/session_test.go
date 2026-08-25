package workspacesurfacehttp

import (
	"errors"
	"testing"
	"time"
)

func TestSurfaceSessionUsesDistinctRandomParentAndFrameCredentials(t *testing.T) {
	store := newSessionStore()
	first, err := store.open(surfaceSession{UserID: "user", WorkspaceID: "workspace", PluginID: "plugin", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.open(surfaceSession{UserID: "user", WorkspaceID: "workspace", PluginID: "plugin", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Credential) < 40 || len(first.FrameToken) < 40 || first.Credential == first.FrameToken || first.Credential == second.Credential || first.FrameToken == second.FrameToken {
		t.Fatalf("session credentials are not distinct random 256-bit values: %+v / %+v", first, second)
	}
}

func TestSurfaceSessionIdleRefreshNeverExtendsAbsoluteExpiry(t *testing.T) {
	store := newSessionStore()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.ttl = time.Second
	store.absoluteTTL = 2500 * time.Millisecond
	record, err := store.open(surfaceSession{UserID: "user", WorkspaceID: "workspace", PluginID: "plugin", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(750 * time.Millisecond)
	refreshed, err := store.credential(record.Credential)
	if err != nil || !refreshed.IdleExpiresAt.Equal(now.Add(time.Second)) {
		t.Fatalf("first refresh = %+v, %v", refreshed, err)
	}
	now = now.Add(900 * time.Millisecond)
	refreshed, err = store.credential(record.Credential)
	if err != nil || !refreshed.IdleExpiresAt.Equal(record.AbsoluteExpiresAt) {
		t.Fatalf("refresh was not clamped to absolute expiry: %+v, %v", refreshed, err)
	}
	now = record.AbsoluteExpiresAt.Add(time.Nanosecond)
	if _, err := store.credential(record.Credential); !errors.Is(err, errSessionUnknown) {
		t.Fatalf("absolute expiry error = %v", err)
	}
}
