package connections

import (
	"os"
	"testing"
)

func TestStore_SaveLoadDelete(t *testing.T) {
	s := NewStore(t.TempDir())

	// Empty store loads as (nil, nil).
	got, err := s.Load()
	if err != nil || got != nil {
		t.Fatalf("empty Load = %v, %v; want nil, nil", got, err)
	}

	c := &Connection{
		ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", Email: "a@b.com",
		Grants: map[ProductKey]*ProductGrant{
			ProductGmail: {ConnectionID: "c1", Product: ProductGmail, Health: HealthHealthy, CredentialRef: "vault://x"},
		},
	}
	if err := s.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err = s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil || got.Subject != "sub-1" || got.Email != "a@b.com" || got.GrantHealthOf(ProductGmail) != HealthHealthy {
		t.Fatalf("round-trip = %+v", got)
	}

	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, _ := s.Load(); got != nil {
		t.Fatal("Delete should remove the record")
	}
	// Idempotent.
	if err := s.Delete(); err != nil {
		t.Fatalf("second Delete should be a no-op, got %v", err)
	}
}

func TestStore_FilePermsOwnerOnly(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(&Connection{ID: "c", Provider: ProviderGoogle, Subject: "s"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("connection file perms = %o, want 600", perm)
	}
}

func TestStore_SaveNil(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(nil); err == nil {
		t.Fatal("Save(nil) must error")
	}
}
