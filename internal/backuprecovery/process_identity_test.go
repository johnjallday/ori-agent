package backuprecovery

import (
	"os"
	"testing"
)

func TestPinnedProcessRejectsMissingAndNonProviderImages(t *testing.T) {
	for _, process := range []*os.Process{nil, {Pid: 0}, {Pid: -1}} {
		if pinnedResticProcess(process) {
			t.Fatal("missing process accepted")
		}
	}
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = self.Release() }()
	if pinnedResticProcess(self) {
		t.Fatal("test process mistaken for the pinned provider")
	}
}
