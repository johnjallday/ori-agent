package backuprecovery

import "testing"

func TestBackupHelperDispatchDoesNotStartNormalRuntime(t *testing.T) {
	for _, arguments := range [][]string{nil, {"-port=8765"}, {"--no-browser"}} {
		if handled, _ := RunHelperProcess(arguments); handled {
			t.Fatal("normal invocation became a backup helper")
		}
	}
	for _, arguments := range [][]string{{"--ori-backup-helper=unknown"}, {"--ori-backup-helper=reader-v1", "unexpected"}, {"--ori-backup-helper"}} {
		if handled, code := RunHelperProcess(arguments); !handled || code != 2 {
			t.Fatal("invalid helper invocation reached a runtime")
		}
	}
}
