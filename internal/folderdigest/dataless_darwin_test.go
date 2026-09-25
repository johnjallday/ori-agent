//go:build darwin

package folderdigest

import (
	"syscall"
	"testing"
)

func TestIsDatalessSys(t *testing.T) {
	if !isDatalessSys(&syscall.Stat_t{Flags: sfDataless}) {
		t.Errorf("SF_DATALESS flag not recognised")
	}
	if isDatalessSys(&syscall.Stat_t{Flags: 0}) {
		t.Errorf("plain file reported dataless")
	}
}
