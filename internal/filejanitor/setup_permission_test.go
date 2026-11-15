package filejanitor

import (
	"os"
	"testing"
	"time"
)

func TestHasSetupPermission_ReadsOnlyStoredGrant(t *testing.T) {
	service, _ := newTestService(t)
	dir, err := service.store.StateDir("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := service.HasSetupPermission("ws-1"); err != nil || allowed {
		t.Fatalf("unconfigured permission = %t, %v", allowed, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read created a state directory", err)
	}
	settings := configuredSettings(t, "ws-1")
	for _, revoked := range []bool{false, true} {
		if revoked {
			settings.AccessRevokedAt = time.Now()
		}
		if err := service.store.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		if allowed, err := service.HasSetupPermission("ws-1"); err != nil || allowed == revoked {
			t.Fatalf("revoked=%t permission=%t, %v", revoked, allowed, err)
		}
	}
}
