//go:build !darwin && !linux

package settingsreset

import "os"

func openArchiveParent(string) (*os.File, error)            { return nil, errArchiveVolume }
func archiveFileAt(*os.Root, string) (*os.File, error)      { return nil, errArchiveVolume }
func archiveEntryPath(string) (archiveEntry, error)         { return archiveEntry{}, errArchiveVolume }
func archiveInodeAt(*os.File, string) (archiveEntry, error) { return archiveEntry{}, errArchiveVolume }
func archiveLocalVolume(*os.File) bool                      { return false }
func archiveOwner() uint32                                  { return 0 }
