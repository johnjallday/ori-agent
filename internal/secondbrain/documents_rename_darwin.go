package secondbrain

import (
	"golang.org/x/sys/unix"
	"os"
)

func publishNewProject(parent *os.File, stage, name string) error {
	return unix.RenameatxNp(int(parent.Fd()), stage, int(parent.Fd()), name, unix.RENAME_EXCL)
}
