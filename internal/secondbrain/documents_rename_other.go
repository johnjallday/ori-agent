//go:build !darwin && !linux

package secondbrain

import "os"

func publishNewProject(*os.File, string, string) error { return ErrOutsideOwnedFolders }
